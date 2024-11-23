package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	logger "log"
	pb "orisun/src/orisun/eventstore"
	"runtime/debug"

	"github.com/nats-io/nats-server/v2/server"
	c "orisun/src/orisun/config"
	dbase "orisun/src/orisun/db"
	l "orisun/src/orisun/logging"
	postgres "orisun/src/orisun/postgres"
)

var AppLogger l.Logger

func main() {
	defer logger.Println("Server shutting down")

	// Load configuration
	config, err := c.LoadConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	logr, err := l.ZapLogger(config.Logging.Level, config.Prod)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	AppLogger = logr

	// Connect to PostgreSQL
	db, err := sql.Open("postgres", fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DB.Host, config.DB.Port, config.DB.User, config.DB.Password, config.DB.Name,
	))
	if err != nil {
		AppLogger.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Run database migrations
	for _, schema := range config.DB.GetSchemas() {
		if err := dbase.RunDbScripts(db, schema, ctx); err != nil {
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

	// Only add cluster configuration if a cluster name is provided
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

	// Connect to NATS
	nc, err := nats.Connect(natsServer.ClientURL())
	if err != nil {
		AppLogger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// Create JetStream context
	js, err := nc.JetStream()
	if err != nil {
		AppLogger.Fatalf("Failed to create JetStream context: %v", err)
	}

	// Create EventStore server and start polling events from Postgres to NATS
	eventStore := pb.NewPostgresEventStoreServer(
		js,
		postgres.NewPostgresSaveEvents(db, AppLogger),
		postgres.NewPostgresGetEvents(db, AppLogger),
		config.DB.GetSchemas(),
	)

	defer cancel()

	for _, schema := range config.DB.GetSchemas() {
		// Get last published position
		lastPosition, err := pb.GetLastPublishedPosition(js, schema)
		if err != nil {
			AppLogger.Fatalf("Failed to get last published position: %v", err)
		}
		go postgres.PollEventsFromPgToNats(ctx, db, js, eventStore, config.PollingPublisher.BatchSize,
			lastPosition, pb.EventsSubjectName, AppLogger, schema)
	}

	// Set up gRPC server with error handling
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(recoveryInterceptor),
		grpc.StreamInterceptor(streamErrorInterceptor),
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
	AppLogger.Info("Grpc Server listening on port %s", config.Grpc.Port)
	if err := grpcServer.Serve(lis); err != nil {
		AppLogger.Fatalf("Failed to serve: %v", err)
	}
}

func streamErrorInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	if err != nil {
		AppLogger.Errorf("Error in streaming RPC %s: %v", info.FullMethod, err)
		return status.Errorf(codes.Internal, "Internal server error")
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
			AppLogger.Errorf("Warning: invalid route URL %q: %v", route, err)
			continue
		}
		urls = append(urls, u)
	}
	return urls
}
