package io.orisun.client;

import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import io.grpc.testing.GrpcCleanupRule;
import eventstore.*;
import eventstore.Eventstore.*;
import org.junit.Rule;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import com.google.protobuf.ByteString;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;

class OrisunClientTest {
    @Rule
    public final GrpcCleanupRule grpcCleanup = new GrpcCleanupRule();

    private MockEventStoreService mockService;
    private OrisunClient client;

    @BeforeEach
    void setUp() throws Exception {
        String serverName = InProcessServerBuilder.generateName();
        mockService = new MockEventStoreService();

        // Create and start the in-process server
        grpcCleanup.register(InProcessServerBuilder
                .forName(serverName)
                .directExecutor()
                .addService(mockService)
                .build()
                .start());

        // Create the client
        client = OrisunClient.newBuilder()
                .withChannel(
                        grpcCleanup.register(
                                InProcessChannelBuilder.forName(serverName)
                                        .directExecutor()
                                        .build()
                        )
                )
                .build();
    }

    @Test
    void testSaveEvents() throws Exception {
        // Prepare test data
        String eventId = UUID.randomUUID().toString();
        Eventstore.SaveEventsRequest request = SaveEventsRequest.newBuilder()
                .setBoundary("users")
                .addEvents(EventToSave.newBuilder()
                        .setEventId(eventId)
                        .setEventType("UserCreated")
                        .setData("{\"username\":\"test\"}")
                        .addTags(Tag.newBuilder()
                                .setKey("aggregate_id")
                                .setValue("user-123")
                                .build())
                        .build())
                .build();

        // Configure mock response
        mockService.setNextWriteResult(WriteResult.newBuilder()
                .setLogPosition(Position.newBuilder()
                        .setCommitPosition(1)
                        .setPreparePosition(1)
                        .build())
                .build());

        // Execute test
        WriteResult result = client.saveEvents(request);

        // Verify results
        assertNotNull(result);
        assertEquals(1, result.getLogPosition().getCommitPosition());
        assertEquals(1, result.getLogPosition().getPreparePosition());
        assertEquals(request, mockService.getLastSaveEventsRequest());
    }

    @Test
    void testSubscribeToEvents() throws Exception {
        CountDownLatch eventLatch = new CountDownLatch(1);
        List<Event> receivedEvents = new ArrayList<>();

        // Prepare subscription request
        SubscribeToEventStoreRequest request = SubscribeToEventStoreRequest.newBuilder()
                .setBoundary("users")
                .build();

        // Set up subscription
        try (EventSubscription subscription = client.subscribeToEvents(request,
                new EventSubscription.EventHandler() {
                    @Override
                    public void onEvent(Event event) {
                        receivedEvents.add(event);
                        eventLatch.countDown();
                    }

                    @Override
                    public void onError(Throwable error) {
                        fail("Unexpected error: " + error);
                    }

                    @Override
                    public void onCompleted() {
                        // Not expected in this test
                    }
                })) {

            // Simulate server sending an event
            mockService.sendEvent(Event.newBuilder()
                    .setEventId(UUID.randomUUID().toString())
                    .setEventType("UserCreated")
                    .build());

            // Wait for event to be received
            assertTrue(eventLatch.await(5, TimeUnit.SECONDS));
            assertEquals(1, receivedEvents.size());
            assertEquals("UserCreated", receivedEvents.get(0).getEventType());
        }
    }

    @Test
    void testSubscribeToPubSub() throws Exception {
        final var messageLatch = new CountDownLatch(1);
        final var receivedMessages = new ArrayList<SubscribeResponse>();

        // Prepare subscription request
        SubscribeRequest request = SubscribeRequest.newBuilder()
                .setSubject("test-subject")
                .setConsumerName("test-consumer")
                .build();

        // Set up subscription
        try (PubSubSubscription subscription = client.subscribeToPubSub(request,
                new PubSubSubscription.MessageHandler() {
                    @Override
                    public void onMessage(SubscribeResponse message) {
                        receivedMessages.add(message);
                        messageLatch.countDown();
                    }

                    @Override
                    public void onError(Throwable error) {
                        fail("Unexpected error: " + error);
                    }

                    @Override
                    public void onCompleted() {
                        // Not expected in this test
                    }
                })
        ) {

            // Simulate server sending a message
            mockService.sendPubSubMessage(Message.newBuilder()
                    .setId(UUID.randomUUID().toString())
                    .setSubject("test-subject")
                    .setData(ByteString.copyFromUtf8("test data"))
                    .build());

            // Wait for message to be received
            assertTrue(messageLatch.await(5, TimeUnit.SECONDS));
            assertEquals(1, receivedMessages.size());
            assertEquals("test-subject", receivedMessages.get(0).getMessage().getSubject());
        }
    }

    // Mock service implementation
    private static class MockEventStoreService extends EventStoreGrpc.EventStoreImplBase {
        private WriteResult nextWriteResult;
        private SaveEventsRequest lastSaveEventsRequest;
        private StreamObserver<Event> eventObserver;
        private StreamObserver<SubscribeResponse> pubSubObserver;

        void setNextWriteResult(WriteResult result) {
            this.nextWriteResult = result;
        }

        SaveEventsRequest getLastSaveEventsRequest() {
            return lastSaveEventsRequest;
        }

        void sendEvent(Event event) {
            if (eventObserver != null) {
                eventObserver.onNext(event);
            }
        }

        void sendPubSubMessage(Message message) {
            if (pubSubObserver != null) {
                pubSubObserver.onNext(SubscribeResponse.newBuilder()
                        .setMessage(message)
                        .build());
            }
        }

        @Override
        public void saveEvents(SaveEventsRequest request, StreamObserver<WriteResult> responseObserver) {
            lastSaveEventsRequest = request;
            responseObserver.onNext(nextWriteResult);
            responseObserver.onCompleted();
        }

        @Override
        public void subscribeToEvents(SubscribeToEventStoreRequest request, StreamObserver<Event> responseObserver) {
            this.eventObserver = responseObserver;
        }

        @Override
        public void subscribeToPubSub(SubscribeRequest request, StreamObserver<SubscribeResponse> responseObserver) {
            this.pubSubObserver = responseObserver;
        }
    }
}
