// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v5.27.3
// source: eventstore.proto

package eventstore

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	EventStore_SaveEvents_FullMethodName               = "/eventstore.EventStore/SaveEvents"
	EventStore_GetEvents_FullMethodName                = "/eventstore.EventStore/GetEvents"
	EventStore_CatchUpSubscribeToEvents_FullMethodName = "/eventstore.EventStore/CatchUpSubscribeToEvents"
	EventStore_PublishToPubSub_FullMethodName          = "/eventstore.EventStore/PublishToPubSub"
	EventStore_SubscribeToPubSub_FullMethodName        = "/eventstore.EventStore/SubscribeToPubSub"
)

// EventStoreClient is the client API for EventStore service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type EventStoreClient interface {
	SaveEvents(ctx context.Context, in *SaveEventsRequest, opts ...grpc.CallOption) (*WriteResult, error)
	GetEvents(ctx context.Context, in *GetEventsRequest, opts ...grpc.CallOption) (*GetEventsResponse, error)
	CatchUpSubscribeToEvents(ctx context.Context, in *CatchUpSubscribeToEventStoreRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Event], error)
	PublishToPubSub(ctx context.Context, in *PublishRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	SubscribeToPubSub(ctx context.Context, in *SubscribeRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[SubscribeResponse], error)
}

type eventStoreClient struct {
	cc grpc.ClientConnInterface
}

func NewEventStoreClient(cc grpc.ClientConnInterface) EventStoreClient {
	return &eventStoreClient{cc}
}

func (c *eventStoreClient) SaveEvents(ctx context.Context, in *SaveEventsRequest, opts ...grpc.CallOption) (*WriteResult, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(WriteResult)
	err := c.cc.Invoke(ctx, EventStore_SaveEvents_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *eventStoreClient) GetEvents(ctx context.Context, in *GetEventsRequest, opts ...grpc.CallOption) (*GetEventsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetEventsResponse)
	err := c.cc.Invoke(ctx, EventStore_GetEvents_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *eventStoreClient) CatchUpSubscribeToEvents(ctx context.Context, in *CatchUpSubscribeToEventStoreRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Event], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &EventStore_ServiceDesc.Streams[0], EventStore_CatchUpSubscribeToEvents_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[CatchUpSubscribeToEventStoreRequest, Event]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type EventStore_CatchUpSubscribeToEventsClient = grpc.ServerStreamingClient[Event]

func (c *eventStoreClient) PublishToPubSub(ctx context.Context, in *PublishRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, EventStore_PublishToPubSub_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *eventStoreClient) SubscribeToPubSub(ctx context.Context, in *SubscribeRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[SubscribeResponse], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &EventStore_ServiceDesc.Streams[1], EventStore_SubscribeToPubSub_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[SubscribeRequest, SubscribeResponse]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type EventStore_SubscribeToPubSubClient = grpc.ServerStreamingClient[SubscribeResponse]

// EventStoreServer is the server API for EventStore service.
// All implementations must embed UnimplementedEventStoreServer
// for forward compatibility.
type EventStoreServer interface {
	SaveEvents(context.Context, *SaveEventsRequest) (*WriteResult, error)
	GetEvents(context.Context, *GetEventsRequest) (*GetEventsResponse, error)
	CatchUpSubscribeToEvents(*CatchUpSubscribeToEventStoreRequest, grpc.ServerStreamingServer[Event]) error
	PublishToPubSub(context.Context, *PublishRequest) (*emptypb.Empty, error)
	SubscribeToPubSub(*SubscribeRequest, grpc.ServerStreamingServer[SubscribeResponse]) error
	mustEmbedUnimplementedEventStoreServer()
}

// UnimplementedEventStoreServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedEventStoreServer struct{}

func (UnimplementedEventStoreServer) SaveEvents(context.Context, *SaveEventsRequest) (*WriteResult, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SaveEvents not implemented")
}
func (UnimplementedEventStoreServer) GetEvents(context.Context, *GetEventsRequest) (*GetEventsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetEvents not implemented")
}
func (UnimplementedEventStoreServer) CatchUpSubscribeToEvents(*CatchUpSubscribeToEventStoreRequest, grpc.ServerStreamingServer[Event]) error {
	return status.Errorf(codes.Unimplemented, "method CatchUpSubscribeToEvents not implemented")
}
func (UnimplementedEventStoreServer) PublishToPubSub(context.Context, *PublishRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PublishToPubSub not implemented")
}
func (UnimplementedEventStoreServer) SubscribeToPubSub(*SubscribeRequest, grpc.ServerStreamingServer[SubscribeResponse]) error {
	return status.Errorf(codes.Unimplemented, "method SubscribeToPubSub not implemented")
}
func (UnimplementedEventStoreServer) mustEmbedUnimplementedEventStoreServer() {}
func (UnimplementedEventStoreServer) testEmbeddedByValue()                    {}

// UnsafeEventStoreServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to EventStoreServer will
// result in compilation errors.
type UnsafeEventStoreServer interface {
	mustEmbedUnimplementedEventStoreServer()
}

func RegisterEventStoreServer(s grpc.ServiceRegistrar, srv EventStoreServer) {
	// If the following call pancis, it indicates UnimplementedEventStoreServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&EventStore_ServiceDesc, srv)
}

func _EventStore_SaveEvents_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SaveEventsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EventStoreServer).SaveEvents(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EventStore_SaveEvents_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EventStoreServer).SaveEvents(ctx, req.(*SaveEventsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EventStore_GetEvents_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetEventsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EventStoreServer).GetEvents(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EventStore_GetEvents_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EventStoreServer).GetEvents(ctx, req.(*GetEventsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EventStore_CatchUpSubscribeToEvents_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(CatchUpSubscribeToEventStoreRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(EventStoreServer).CatchUpSubscribeToEvents(m, &grpc.GenericServerStream[CatchUpSubscribeToEventStoreRequest, Event]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type EventStore_CatchUpSubscribeToEventsServer = grpc.ServerStreamingServer[Event]

func _EventStore_PublishToPubSub_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PublishRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EventStoreServer).PublishToPubSub(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EventStore_PublishToPubSub_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EventStoreServer).PublishToPubSub(ctx, req.(*PublishRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EventStore_SubscribeToPubSub_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(SubscribeRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(EventStoreServer).SubscribeToPubSub(m, &grpc.GenericServerStream[SubscribeRequest, SubscribeResponse]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type EventStore_SubscribeToPubSubServer = grpc.ServerStreamingServer[SubscribeResponse]

// EventStore_ServiceDesc is the grpc.ServiceDesc for EventStore service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var EventStore_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "eventstore.EventStore",
	HandlerType: (*EventStoreServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SaveEvents",
			Handler:    _EventStore_SaveEvents_Handler,
		},
		{
			MethodName: "GetEvents",
			Handler:    _EventStore_GetEvents_Handler,
		},
		{
			MethodName: "PublishToPubSub",
			Handler:    _EventStore_PublishToPubSub_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "CatchUpSubscribeToEvents",
			Handler:       _EventStore_CatchUpSubscribeToEvents_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "SubscribeToPubSub",
			Handler:       _EventStore_SubscribeToPubSub_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "eventstore.proto",
}
