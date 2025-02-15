package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	// "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	logger "log"
	pb "orisun/src/orisun/eventstore"
	"runtime/debug"

	auth "orisun/src/orisun/auth"
	c "orisun/src/orisun/config"
	dbase "orisun/src/orisun/db"
	l "orisun/src/orisun/logging"
	postgres "orisun/src/orisun/postgres"

	admin "orisun/src/orisun/admin"

	"github.com/nats-io/nats-server/v2/server"
)

var AppLogger l.Logger

type pgLockProvider struct {
	db *sql.DB
}

func (m *pgLockProvider) Lock(ctx context.Context, lockName string) (pb.UnlockFunc, error) {
	AppLogger.Debugf("Lock called for: %v", lockName)
	conn, err := m.db.Conn(ctx)

	if err != nil {
		return nil, err
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})

	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256([]byte(lockName))
	lockID := int64(binary.BigEndian.Uint64(hash[:]))

	var acquired bool

	err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock($1)", int32(lockID)).Scan(&acquired)

	if err != nil {
		AppLogger.Errorf("Failed to acquire lock: %v, will retry", err)
		return nil, err
	}

	if !acquired {
		AppLogger.Warnf("Failed to acquire lock within timeout")
		return nil, errors.New("lock acquisition timed out")
	}

	unlockFunc := func() error {
		AppLogger.Debugf("Unlock called for: %v", lockName)
		defer conn.Close()
		defer tx.Rollback()

		return nil
	}

	return unlockFunc, nil
}

func main() {
	defer logger.Println("Server shutting down")

	// Load configuration
	config, err := c.LoadConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	logr, err := l.ZapLogger(config.Logging.Level)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	AppLogger = logr

	// Connect to PostgreSQL
	db, err := sql.Open("postgres", fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.Postgres.Host, config.Postgres.Port, config.Postgres.User, config.Postgres.Password, config.Postgres.Name,
	))
	if err != nil {
		AppLogger.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	var postgesBoundarySchemaMappings = config.Postgres.GetSchemaMapping()

	// Run database migrations
	for _, schema := range postgesBoundarySchemaMappings {
		isAdminSchema := schema.Schema == config.Admin.Schema
		if err := dbase.RunDbScripts(db, schema.Schema, isAdminSchema, ctx); err != nil {
			AppLogger.Fatalf("Failed to run database migrations for schema %s: %v", schema, err)
		}
		AppLogger.Info("Database migrations for schema %s completed successfully", schema)
	}

	natsOptions := &server.Options{
		ServerName: "ORISUN-" + uuid.New().String(),
		Port:       config.Nats.Port,
		MaxPayload: config.Nats.MaxPayload,
		// MaxConnections: config.Nats.MaxConnections,
		JetStream: true,
		StoreDir:  config.Nats.StoreDir,
	}

	// Only add cluster configuration if cluster is enabled
	if config.Nats.Cluster.Enabled {
		natsOptions.Cluster = server.ClusterOpts{
			Name: config.Nats.Cluster.Name,
			Host: config.Nats.Cluster.Host,
			Port: config.Nats.Cluster.Port,
		}
		natsOptions.Routes = convertToURLSlice(config.Nats.Cluster.GetRoutes())

		AppLogger.Info("Nats cluster is enabled, running in clustered mode")
		AppLogger.Info("Cluster configuration: Name=%v, Host=%v, Port=%v, Routes=%v",
			config.Nats.Cluster.Name, config.Nats.Cluster.Host, config.Nats.Cluster.Port, config.Nats.Cluster.Routes)
	} else {
		AppLogger.Info("Nats cluster is disabled, running in standalone mode")
	}

	// Start embedded NATS server with clustering
	natsServer, err := server.NewServer(natsOptions)
	if err != nil {
		AppLogger.Fatalf("Failed to create NATS server: %v", err)
	}

	go natsServer.Start()
	if !natsServer.ReadyForConnections(config.Nats.Cluster.Timeout) {
		AppLogger.Fatal("NATS server failed to start")
	}
	defer natsServer.Shutdown()
	AppLogger.Info("NATS server started on ", natsServer.ClientURL())

	// Connect to NATS
	nc, err := nats.Connect(natsServer.ClientURL())
	if err != nil {
		AppLogger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		AppLogger.Fatalf("Failed to create JetStream context: %v", err)
	}

	//wait for JetSteam system to be available, retry until it is available
	jetStreamTestDone := make(chan struct{})

	go func() {
		defer close(jetStreamTestDone)

		for {
			streamName := "test_jetstream"
			_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
				Name: streamName,
				Subjects: []string{
					streamName + ".test",
				},
				MaxMsgs: 1,
			})

			if err != nil {
				AppLogger.Warnf("failed to add stream: %v %v", streamName, err)
				time.Sleep(5 * time.Second)
				continue
			}

			r, err := js.Publish(ctx, streamName+".test", []byte("test"))
			if err != nil {
				AppLogger.Warnf("Failed to publish to JetStream, retrying in 5 second: %v", err)
				time.Sleep(5 * time.Second)
			} else {
				AppLogger.Infof("Published to JetStream: %v", r)
				break
			}
		}
	}()

	select {
	case <-jetStreamTestDone:
		AppLogger.Info("JetStream system is available")
	}

	// Create EventStore grpc server
	eventStore := pb.NewEventStoreServer(
		ctx,
		js,
		postgres.NewPostgresSaveEvents(db, &AppLogger, postgesBoundarySchemaMappings),
		postgres.NewPostgresGetEvents(db, &AppLogger, postgesBoundarySchemaMappings),
		&pgLockProvider{
			db: db,
		},
		getBoundaryNames(&config.Boundaries),
	)

	defer cancel()

	//poll events from Postgres to NATS
	for _, schema := range config.Postgres.GetSchemaMapping() {
		// Get last published position
		lastPosition, err := pb.GetLastPublishedPosition(ctx, js, schema.Boundary)
		if err != nil {
			AppLogger.Fatalf("Failed to get last published position: %v", err)
		}
		AppLogger.Info("Last published position for schema %v: %v", schema, lastPosition)
		go func() {
			postgres.PollEventsFromPgToNats(
				ctx,
				db,
				js,
				postgres.NewPostgresGetEvents(db, &AppLogger, postgesBoundarySchemaMappings),
				config.PollingPublisher.BatchSize,
				lastPosition,
				AppLogger,
				schema.Boundary,
				schema.Schema,
			)
		}()
	}

	// Start admin server
	go func() {
		adminServer := admin.NewAdminServer(db, AppLogger, eventStore, config.Admin.Schema)

		httpServer := &http.Server{
			Addr:    fmt.Sprintf(":%s", config.Admin.Port),
			Handler: adminServer,
		}

		AppLogger.Info("Starting admin server on port %s", config.Admin.Port)
		if err := httpServer.ListenAndServe(); err != nil {
			AppLogger.Errorf("Admin server error: %v", err)
		}
	}()

	// Initialize authenticator with default admin user
	authenticator := auth.NewAuthenticator([]auth.User{
		{
			Username: config.Auth.AdminUsername,
			Password: config.Auth.AdminPassword,
			Roles:    []auth.Role{auth.RoleAdmin},
		},
	})

	// Set up gRPC server with error handling
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryAuthInterceptor(authenticator)),
		grpc.StreamInterceptor(auth.StreamAuthInterceptor(authenticator)),
		grpc.ChainUnaryInterceptor(recoveryInterceptor),
		grpc.ChainStreamInterceptor(streamErrorInterceptor),
	)
	pb.RegisterEventStoreServer(grpcServer, eventStore)

	// Enable reflection
	if config.Grpc.EnableReflection {
		AppLogger.Info("Enabling gRPC server reflection")
		reflection.Register(grpcServer)
	}

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", config.Grpc.Port))
	if err != nil {
		AppLogger.Fatalf("Failed to listen: %v", err)
	}

	go func() {
		// clientConn, err := grpc.NewClient(fmt.Sprintf("dns:///localhost:%s", config.Grpc.Port),
		// 	grpc.WithTransportCredentials(insecure.NewCredentials()),
		// )

		if err != nil {
			AppLogger.Fatalf("Failed to create gRPC client: %v", err)
		}
		// defer clientConn.Close()
		// eventStoreClient := pb.NewEventStoreClient(clientConn)

		// Create an authenticated context
		// ctxx := getAuthenticatedContext(config.Auth.AdminUsername, config.Auth.AdminPassword)
		// userProjector := admin.NewUserProjector(db, AppLogger, eventStoreClient, config.Admin.Schema, config.Auth.AdminUsername, config.Auth.AdminPassword)
		// err = userProjector.Start(ctxx)

		if err != nil {
			AppLogger.Fatalf("Failed to start projection %v", err)
		}
	}()

	AppLogger.Info("Grpc Server listening on port %s", config.Grpc.Port)
	if err := grpcServer.Serve(lis); err != nil {
		AppLogger.Fatalf("Failed to serve: %v", err)
	}
}

func getBoundaryNames(boundary *[]c.Boundary) *[]string {
	var names []string
	for _, boundary := range *boundary {
		names = append(names, boundary.Name)
	}
	return &names
}

func streamErrorInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	if err != nil {
		AppLogger.Errorf("Error in streaming RPC %s: %v", info.FullMethod, err)
		return status.Errorf(codes.Internal, "Error: %v", err)
	}
	return nil
}

func recoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			AppLogger.Errorf("Panic in %s: %v\nStack Trace:\n%s", info.FullMethod, r, debug.Stack())
			err = status.Errorf(codes.Internal, "Internal server error")
		}
	}()
	return handler(ctx, req)
}

func convertToURLSlice(routes []string) []*url.URL {
	var urls []*url.URL
	for _, route := range routes {
		u, err := url.Parse(route)
		if err != nil {
			AppLogger.Fatalf("Warning: invalid route URL %q: %v", route, err)
			continue
		}
		urls = append(urls, u)
	}
	return urls
}

func createBasicAuthHeader(username, password string) string {
	auth := username + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

func getAuthenticatedContext(username, password string) context.Context {
	// Create Basic Auth header
	authHeader := createBasicAuthHeader(username, password)

	// Create metadata with the Authorization header
	md := metadata.New(map[string]string{
		"Authorization": authHeader,
	})

	// Attach metadata to the context
	return metadata.NewOutgoingContext(context.Background(), md)
}
