package admin

import (
    "context"
    "encoding/base64"
    "strings"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/metadata"
    "google.golang.org/grpc/status"
)

type userContextKey struct{}

func UnaryAuthInterceptor(auth *Authenticator) grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
        user, err := authenticate(ctx, auth)
        if err != nil {
            return nil, err
        }

        // Check required role based on method
        requiredRole := getRequiredRole(info.FullMethod)
        if !auth.HasRole(user, requiredRole) {
            return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
        }

        newCtx := context.WithValue(ctx, userContextKey{}, user)
        return handler(newCtx, req)
    }
}

func StreamAuthInterceptor(auth *Authenticator) grpc.StreamServerInterceptor {
    return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
        user, err := authenticate(ss.Context(), auth)
        if err != nil {
            return err
        }

        requiredRole := getRequiredRole(info.FullMethod)
        if !auth.HasRole(user, requiredRole) {
            return status.Error(codes.PermissionDenied, "insufficient permissions")
        }

        newCtx := context.WithValue(ss.Context(), userContextKey{}, user)
        wrapped := newWrappedServerStream(ss, newCtx)
        return handler(srv, wrapped)
    }
}

func authenticate(ctx context.Context, auth *Authenticator) (User, error) {
    md, ok := metadata.FromIncomingContext(ctx)
    if !ok {
        return User{}, status.Error(codes.Unauthenticated, "missing metadata")
    }

    values := md.Get("authorization")
    if len(values) == 0 {
        return User{}, status.Error(codes.Unauthenticated, "missing authorization header")
    }

    authHeader := values[0]
    if !strings.HasPrefix(authHeader, "Basic ") {
        return User{}, status.Error(codes.Unauthenticated, "invalid authorization header format")
    }

    encoded := strings.TrimPrefix(authHeader, "Basic ")
    decoded, err := base64.StdEncoding.DecodeString(encoded)
    if err != nil {
        return User{}, status.Error(codes.Unauthenticated, "invalid authorization header encoding")
    }

    credentials := strings.SplitN(string(decoded), ":", 2)
    if len(credentials) != 2 {
        return User{}, status.Error(codes.Unauthenticated, "invalid authorization header format")
    }

    user, err := auth.ValidateCredentials(credentials[0], credentials[1])
    if err != nil {
        return User{}, status.Error(codes.Unauthenticated, "invalid credentials")
    }

    return user, nil
}

func getRequiredRole(method string) Role {
    switch method {
    case "/eventstore.EventStore/SaveEvents":
        return RoleWrite
    case "/eventstore.EventStore/GetEvents", 
         "/eventstore.EventStore/SubscribeToEvents":
        return RoleRead
    default:
        return RoleOperations
    }
} 

type wrappedServerStream struct {
    grpc.ServerStream
    ctx context.Context
}

func newWrappedServerStream(ss grpc.ServerStream, ctx context.Context) grpc.ServerStream {
    return &wrappedServerStream{
        ServerStream: ss,
        ctx:         ctx,
    }
}

func (w *wrappedServerStream) Context() context.Context {
    return w.ctx
}