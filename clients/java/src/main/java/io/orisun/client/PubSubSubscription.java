package io.orisun.client;

import eventstore.EventStoreGrpc;
import eventstore.Eventstore;
import eventstore.Eventstore.*;
import io.grpc.stub.StreamObserver;

import java.util.concurrent.TimeUnit;

public class PubSubSubscription implements AutoCloseable {
    private final StreamObserver<Eventstore.SubscribeResponse> observer;
    private volatile boolean closed = false;

    public interface MessageHandler {
        void onMessage(Eventstore.SubscribeResponse message);

        void onError(Throwable error);

        void onCompleted();
    }

    PubSubSubscription(EventStoreGrpc.EventStoreStub stub,
                       SubscribeRequest request,
                       MessageHandler handler,
                       int timeoutSeconds) {
        this.observer = new StreamObserver<>() {
            @Override
            public void onNext(SubscribeResponse response) {
                if (!closed && response.hasMessage()) {
                    handler.onMessage(response);
                }
            }

            @Override
            public void onError(Throwable t) {
                if (!closed) {
                    handler.onError(t);
                }
            }

            @Override
            public void onCompleted() {
                if (!closed) {
                    handler.onCompleted();
                }
            }
        };
        stub
                .withDeadlineAfter(timeoutSeconds, TimeUnit.SECONDS)
                .subscribeToPubSub(request, this.observer);
    }

    @Override
    public void close() {
        closed = true;
        observer.onCompleted();
    }
} 