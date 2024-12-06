package io.orisun.client;

import io.grpc.*;
import io.grpc.stub.StreamObserver;
import eventstore.*;
import eventstore.Eventstore.*;

import java.util.concurrent.TimeUnit;
import java.util.concurrent.CompletableFuture;

public class OrisunClient implements AutoCloseable {
    private final ManagedChannel channel;
    private final EventStoreGrpc.EventStoreBlockingStub blockingStub;
    private final EventStoreGrpc.EventStoreStub asyncStub;
    private final int defaultTimeoutSeconds;

    public static class Builder {
        private String host = "localhost";
        private int port = 50051;
        private int timeoutSeconds = 30;
        private boolean useTls = false;
        private ManagedChannel channel;

        public Builder withHost(String host) {
            this.host = host;
            return this;
        }

        public Builder withPort(int port) {
            this.port = port;
            return this;
        }

        public Builder withTimeout(int seconds) {
            this.timeoutSeconds = seconds;
            return this;
        }

        public Builder withTls(boolean useTls) {
            this.useTls = useTls;
            return this;
        }

        public Builder withChannel(ManagedChannel channel) {
            this.channel = channel;
            return this;
        }

        public OrisunClient build() {
            if (channel == null) {
                final var newChannel = ManagedChannelBuilder.forAddress(host, port);

                if (!useTls) {
                    newChannel.usePlaintext();
                }

                channel = newChannel.build();
            }

            return new OrisunClient(channel, timeoutSeconds);
        }
    }

    public static Builder newBuilder() {
        return new Builder();
    }

    private OrisunClient(ManagedChannel channel, int timeoutSeconds) {
        this.channel = channel;
        this.defaultTimeoutSeconds = timeoutSeconds;
        this.blockingStub = EventStoreGrpc.newBlockingStub(channel);
        this.asyncStub = EventStoreGrpc.newStub(channel);
    }

    // Synchronous methods
    public WriteResult saveEvents(final SaveEventsRequest request) throws OrisunException {
        try {
            return blockingStub
                    .withDeadlineAfter(defaultTimeoutSeconds, TimeUnit.SECONDS)
                    .saveEvents(request);
        } catch (StatusRuntimeException e) {
            throw new OrisunException("Failed to save events", e);
        }
    }

    public GetEventsResponse getEvents(GetEventsRequest request) throws OrisunException {
        try {
            return blockingStub
                    .withDeadlineAfter(defaultTimeoutSeconds, TimeUnit.SECONDS)
                    .getEvents(request);
        } catch (StatusRuntimeException e) {
            throw new OrisunException("Failed to get events", e);
        }
    }

    // Asynchronous methods
    public CompletableFuture<WriteResult> saveEventsAsync(SaveEventsRequest request) {
        CompletableFuture<WriteResult> future = new CompletableFuture<>();

        asyncStub
                .withDeadlineAfter(defaultTimeoutSeconds, TimeUnit.SECONDS)
                .saveEvents(request, new StreamObserver<WriteResult>() {
                    @Override
                    public void onNext(WriteResult result) {
                        future.complete(result);
                    }

                    @Override
                    public void onError(Throwable t) {
                        future.completeExceptionally(new OrisunException("Failed to save events", t));
                    }

                    @Override
                    public void onCompleted() {
                        // Already completed in onNext
                    }
                });

        return future;
    }

    // Streaming methods
    public EventSubscription subscribeToEvents(SubscribeToEventStoreRequest request,
                                               EventSubscription.EventHandler handler) {
        return new EventSubscription(asyncStub, request, handler, defaultTimeoutSeconds);
    }

    public PubSubSubscription subscribeToPubSub(SubscribeRequest request,
                                                PubSubSubscription.MessageHandler handler) {
        return new PubSubSubscription(asyncStub, request, handler, defaultTimeoutSeconds);
    }

    public void publishToPubSub(PublishRequest request) throws OrisunException {
        try {
            blockingStub
                    .withDeadlineAfter(defaultTimeoutSeconds, TimeUnit.SECONDS)
                    .publishToPubSub(request);
        } catch (StatusRuntimeException e) {
            throw new OrisunException("Failed to publish message", e);
        }
    }

    @Override
    public void close() {
        if (channel != null && !channel.isShutdown()) {
            try {
                channel.shutdown().awaitTermination(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }
    }
} 