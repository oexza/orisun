package io.orisun.client;

import io.grpc.stub.StreamObserver;
import eventstore.*;
import eventstore.Eventstore.*;
import java.util.concurrent.TimeUnit;

public class EventSubscription implements AutoCloseable {
    private final StreamObserver<Eventstore.Event> observer;
    private volatile boolean closed = false;

    public interface EventHandler {
        void onEvent(Event event);
        void onError(Throwable error);
        void onCompleted();
    }

    EventSubscription(EventStoreGrpc.EventStoreStub stub, 
                     SubscribeToEventStoreRequest request,
                     EventHandler handler,
                     int timeoutSeconds) {
        stub
            .withDeadlineAfter(timeoutSeconds, TimeUnit.SECONDS)
            .subscribeToEvents(request, new StreamObserver<Event>() {
                @Override
                public void onNext(Event event) {
                    if (!closed) {
                        handler.onEvent(event);
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
            });
    }

    @Override
    public void close() {
        closed = true;
        observer.onCompleted();
    }
}
