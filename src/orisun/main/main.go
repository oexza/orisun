package main

import (
	"context"
	"database/sql"
	"encoding/base64"

	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

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

func main() {
	defer logger.Println("Server shutting down")

	// Load configuration and initialize logger
	config := initializeConfig()
	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database
	db := initializeDatabase(config)
	defer db.Close()

	// Initialize NATS
	js, nc, ns := initializeNATS(ctx, config)
	defer nc.Close()
	defer ns.Shutdown()

	// time.Sleep(60 * time.Second)

	// Initialize EventStore
	eventStore, postgesBoundarySchemaMappings := initializeEventStore(ctx, config, db, js)

	// Start polling events
	startEventPolling(ctx, config, db, js, postgesBoundarySchemaMappings)

	// Start admin server
	startAdminServer(config, db, eventStore)

	// Start projectors
	startProjectors(config.Admin.Boundary, config, eventStore, db)

	// Start gRPC server
	startGRPCServer(config, eventStore)
}

func initializeConfig() *c.AppConfig {
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
	return config
}

func initializeDatabase(config *c.AppConfig) *sql.DB {
	db, err := sql.Open(
		"postgres", fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			config.Postgres.Host, config.Postgres.Port, config.Postgres.User, config.Postgres.Password, config.Postgres.Name))
	if err != nil {
		AppLogger.Fatalf("Failed to connect to database: %v", err)
	}

	postgesBoundarySchemaMappings := config.Postgres.GetSchemaMapping()
	for _, schema := range postgesBoundarySchemaMappings {
		isAdminSchema := schema.Schema == config.Admin.Schema
		if err := dbase.RunDbScripts(db, schema.Schema, isAdminSchema, context.Background()); err != nil {
			AppLogger.Fatalf("Failed to run database migrations for schema %s: %v", schema, err)
		}
		AppLogger.Info("Database migrations for schema %s completed successfully", schema)
	}

	return db
}

func initializeNATS(ctx context.Context, config *c.AppConfig) (jetstream.JetStream, *nats.Conn, *server.Server) {
	natsOptions := createNATSOptions(config)
	natsServer := startNATSServer(natsOptions, config)

	// Connect to NATS
	nc, err := nats.Connect("", nats.InProcessServer(natsServer))
	if err != nil {
		AppLogger.Fatalf("Failed to connect to NATS: %v", err)
	}

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		AppLogger.Fatalf("Failed to create JetStream context: %v", err)
	}
	// time.Sleep(30 * time.Second)
	waitForJetStream(ctx, js)
	return js, nc, natsServer
}

func createNATSOptions(config *c.AppConfig) *server.Options {
	options := &server.Options{
		ServerName: config.Nats.ServerName,
		Port:       config.Nats.Port,
		MaxPayload: config.Nats.MaxPayload,
		JetStream:  true,
		StoreDir:   config.Nats.StoreDir,
	}

	if config.Nats.Cluster.Enabled {
		options.Cluster = server.ClusterOpts{
			Name: config.Nats.Cluster.Name,
			Host: config.Nats.Cluster.Host,
			Port: config.Nats.Cluster.Port,
		}
		options.Routes = convertToURLSlice(config.Nats.Cluster.GetRoutes())
		AppLogger.Info("Nats cluster is enabled, running in clustered mode")
		AppLogger.Info(
			"Cluster configuration: Name=%v, Host=%v, Port=%v, Routes=%v",
			config.Nats.Cluster.Name,
			config.Nats.Cluster.Host,
			config.Nats.Cluster.Port,
			config.Nats.Cluster.Routes,
		)
	} else {
		AppLogger.Info("Nats cluster is disabled, running in standalone mode")
	}

	return options
}

func startNATSServer(options *server.Options, config *c.AppConfig) *server.Server {
	natsServer, err := server.NewServer(options)
	if err != nil {
		AppLogger.Fatalf("Failed to create NATS server: %v", err)
	}

	natsServer.ConfigureLogger()
	go natsServer.Start()
	if !natsServer.ReadyForConnections(config.Nats.Cluster.Timeout) {
		AppLogger.Fatal("NATS server failed to start")
	}
	AppLogger.Info("NATS server started on ", natsServer.ClientURL())

	return natsServer
}

func waitForJetStream(ctx context.Context, js jetstream.JetStream) {
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
}

func initializeEventStore(ctx context.Context, config *c.AppConfig, db *sql.DB, js jetstream.JetStream) (*pb.EventStore, map[string]c.BoundaryToPostgresSchemaMapping) {
	AppLogger.Info("Initializing EventStore")
	postgesBoundarySchemaMappings := config.Postgres.GetSchemaMapping()
	eventStore := pb.NewEventStoreServer(
		ctx,
		js,
		postgres.NewPostgresSaveEvents(db, &AppLogger, postgesBoundarySchemaMappings),
		postgres.NewPostgresGetEvents(db, &AppLogger, postgesBoundarySchemaMappings),
		postgres.NewPGLockProvider(db, AppLogger),
		getBoundaryNames(&config.Boundaries),
	)
	AppLogger.Info("EventStore initialized")

	return eventStore, postgesBoundarySchemaMappings
}

func startEventPolling(ctx context.Context, config *c.AppConfig, db *sql.DB, js jetstream.JetStream, postgesBoundarySchemaMappings map[string]c.BoundaryToPostgresSchemaMapping) {
	for _, schema := range config.Postgres.GetSchemaMapping() {
		lockP := postgres.NewPGLockProvider(db, AppLogger)
		unlock, err := lockP.Lock(ctx, schema.Schema)
		if err != nil {
			AppLogger.Fatalf("Failed to acquire lock: %v", err)
		}

		AppLogger.Infof("Successfully acquired polling lock for %v", schema.Schema)

		// Get last published position
		lastPosition, err := pb.GetLastPublishedPositionFromNats(ctx, js, schema.Boundary)
		if err != nil {
			AppLogger.Fatalf("Failed to get last published position: %v", err)
		}
		AppLogger.Info("Last published position for schema %v: %v", schema, lastPosition)

		go func(schema c.BoundaryToPostgresSchemaMapping) {
			defer unlock()
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
		}(schema)
	}
}

func startAdminServer(config *c.AppConfig, db *sql.DB, eventStore *pb.EventStore) {
	go func() {
		adminServer, err := admin.NewAdminServer(db, AppLogger, eventStore, config.Admin.Schema, config.Admin.Boundary)
		if err != nil {
			AppLogger.Fatalf("Could not start admin server %v", err)
		}
		httpServer := &http.Server{
			Addr:    fmt.Sprintf(":%s", config.Admin.Port),
			Handler: adminServer,
		}

		AppLogger.Info("Starting admin server on port %s", config.Admin.Port)
		if err := httpServer.ListenAndServe(); err != nil {
			AppLogger.Errorf("Admin server error: %v", err)
		}
		AppLogger.Info("Admin server started on port %s", config.Admin.Port)
	}()
}

func startGRPCServer(config *c.AppConfig, eventStore pb.EventStoreServer) {
	authenticator := auth.NewAuthenticator([]auth.User{
		{
			Username: config.Auth.AdminUsername,
			Password: config.Auth.AdminPassword,
			Roles:    []auth.Role{auth.RoleAdmin},
		},
	})

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryAuthInterceptor(authenticator)),
		grpc.StreamInterceptor(auth.StreamAuthInterceptor(authenticator)),
		grpc.ChainUnaryInterceptor(recoveryInterceptor),
		grpc.ChainStreamInterceptor(streamErrorInterceptor),
	)
	pb.RegisterEventStoreServer(grpcServer, eventStore)

	if config.Grpc.EnableReflection {
		AppLogger.Info("Enabling gRPC server reflection")
		reflection.Register(grpcServer)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", config.Grpc.Port))
	if err != nil {
		AppLogger.Fatalf("Failed to listen: %v", err)
	}

	AppLogger.Info("Grpc Server listening on port %s", config.Grpc.Port)
	if err := grpcServer.Serve(lis); err != nil {
		AppLogger.Fatalf("Failed to serve: %v", err)
	}
}

func startProjectors(boundary string, config *c.AppConfig, eventStore *pb.EventStore, db *sql.DB) {
	go func() {
		AppLogger.Info("Starting user projector")
		userProjector := admin.NewUserProjector(
			db,
			AppLogger,
			eventStore,
			config.Admin.Schema,
			boundary,
			config.Auth.AdminUsername,
			config.Auth.AdminPassword,
		)
		err := userProjector.Start(context.Background())

		if err != nil {
			AppLogger.Fatalf("Failed to start projection %v", err)
		}
		AppLogger.Info("User projector started")
	}()
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
