# Orisun - A Batteries Included Event Store

## Description
Orisun is a truly batteries-included event sourcing solution with an embedded NATS JetStream server and PostgreSQL support. It provides a reliable, scalable event store with built-in pub/sub capabilities, making it ideal for event-driven architectures and CQRS applications.

### Key Features
- **Embedded NATS JetStream**: No separate NATS installation required
- **Auto Database Setup**: Automatically creates and manages its schema
- **Dynamic Consistency Boundaries (DCB)**: Unlike traditional event stores that use streams as consistency boundaries
- **Global Ordering**: Built-in global ordering guarantee for events
- **Optimistic Concurrency**: Prevents conflicts while allowing parallel event processing
- **Real-time Event Streaming**: Subscribe to event changes in real-time
- **Load Balanced Pub/Sub**: Distribute messages across multiple consumers
- **Flexible Event Querying**: Query events by various criteria including custom tags
- **High Performance**: Efficient PostgreSQL-based storage with embedded NATS JetStream for streaming

## Prerequisites
- PostgreSQL 13+ database

## Quick Start (Using Pre-built Binary)

1. Download the latest release for your platform from the [releases page](https://github.com/yourusername/orisun/releases)

2. Run the binary with environment variables:
```bash
# Minimal configuration
ORISUN_DB_HOST=localhost \
ORISUN_DB_PORT=5432 \
ORISUN_DB_USER=postgres \
ORISUN_DB_PASSWORD=your_password \
ORISUN_DB_NAME=your_database \
ORISUN_DB_SCHEMAS=your_schema \
orisun-[platform]-[arch]

# Example with all options
ORISUN_DB_HOST=localhost \
ORISUN_DB_PORT=5432 \
ORISUN_DB_USER=postgres \
ORISUN_DB_PASSWORD=your_password \
ORISUN_DB_NAME=your_database \
ORISUN_DB_SCHEMAS=your_schema \
ORISUN_GRPC_PORT=50051 \
ORISUN_NATS_PORT=4222 \
ORISUN_NATS_STORE_DIR=/var/opt/nats \
orisun-darwin-arm64
```

Orisun will automatically:
- Set up its database schema
- Start an embedded NATS JetStream server
- Start the gRPC server


## gRPC API Examples

### SaveEvents
Save events with optimistic concurrency control:

```bash
grpcurl -d @ localhost:50051 eventstore.EventStore/SaveEvents <<EOF
{
  "events": [
    {
      "event_id": "evt-123",
      "event_type": "UserCreated",
      "tags": [
        {"key": "aggregate_id", "value": "user-123"},
        {"key": "version", "value": "1"}
      ],
      "data": "{\"username\": \"john_doe\", \"email\": \"john@example.com\"}",
      "metadata": "{\"source\": \"user_service\", \"timestamp\": \"2024-01-01T12:00:00Z\"}"
    }
  ],
  "consistency_condition": {
    "consistency_marker": {
      "commit_position": "0",
      "prepare_position": "0"
    },
    "criteria": {
      "criteria": [
        {
          "tags": [
            {"key": "aggregate_id", "value": "user-123"}
          ]
        }
      ]
    }
  },
  "boundary": "users"
}
EOF
```

### GetEvents
Query events with criteria:

```bash
grpcurl -d @ localhost:50051 eventstore.EventStore/GetEvents <<EOF
{
  "criteria": {
    "criteria": [
      {
        "tags": [
          {"key": "aggregate_id", "value": "user-123"}
        ]
      }
    ]
  },
  "count": 100,
  "direction": "ASC",
  "last_retrieved_position": {
    "commit_position": "0",
    "prepare_position": "0"
  }
}
EOF
```

### SubscribeToEvents
Subscribe to real-time event updates:

```bash
grpcurl -d @ localhost:50051 eventstore.EventStore/SubscribeToEvents <<EOF
{
  "subscriber_name": "my-subscriber",
  "criteria": {
    "criteria": [
      {
        "tags": [
          {"key": "aggregate_id", "value": "user-123"}
        ]
      }
    ]
  },
  "boundary": "users",
  "position": {
    "commit_position": "0",
    "prepare_position": "0"
  }
}
EOF
```

### PublishToPubSub
Publish a message to a pub/sub topic:

```bash
grpcurl -d @ localhost:50051 eventstore.EventStore/PublishToPubSub <<EOF
{
  "subject": "notifications",
  "data": "{\"message\": \"Hello World\"}",
  "metadata": "{\"priority\": \"high\"}"
}
EOF
```

### SubscribeToPubSub
Subscribe to messages from a pub/sub topic:

```bash
grpcurl -d @ localhost:50051 eventstore.EventStore/SubscribeToPubSub <<EOF
{
  "subject": "notifications",
  "consumer_name": "notification-processor"
}
EOF
```

## Common Use Cases

### Event Sourcing Pattern
```bash
# 1. Save an event
grpcurl -d @ localhost:50051 eventstore.EventStore/SaveEvents <<EOF
{
  "events": [
    {
      "event_id": "evt-123",
      "event_type": "AccountCreated",
      "tags": [
        {"key": "aggregate_id", "value": "account-123"},
        {"key": "version", "value": "1"}
      ],
      "data": "{\"initial_balance\": 1000}"
    }
  ],
  "boundary": "accounts"
}
EOF

# 2. Subscribe to account events
grpcurl -d @ localhost:50051 eventstore.EventStore/SubscribeToEvents <<EOF
{
  "subscriber_name": "account-processor",
  "criteria": {
    "criteria": [
      {
        "tags": [
          {"key": "aggregate_id", "value": "account-123"}
        ]
      }
    ]
  },
  "boundary": "accounts"
}
EOF
```

### Load Balanced Processing
```bash
# Start multiple subscribers with the same consumer_name for load balancing
grpcurl -d @ localhost:50051 eventstore.EventStore/SubscribeToPubSub <<EOF
{
  "subject": "orders",
  "consumer_name": "order-processor"
}
EOF
```

## Error Handling
Common error responses:
- `ALREADY_EXISTS`: Consistency condition violation
- `INVALID_ARGUMENT`: Missing required fields
- `INTERNAL`: Database or system errors
- `NOT_FOUND`: Stream or consumer not found

## Building from Source

### Prerequisites
- Go 1.20+
- Make

1. Clone the repository:
```bash
git clone https://github.com/yourusername/orisun.git
cd orisun
```

2. Build the binary:
```bash
./build.sh
```

3. Run the built binary:
```bash
ORISUN_DB_HOST=localhost \
ORISUN_DB_PORT=5432 \
ORISUN_DB_USER=postgres \
ORISUN_DB_PASSWORD=your_password \
ORISUN_DB_NAME=your_database \
ORISUN_DB_SCHEMAS=your_schema \
./orisun
```

## Usage

### Starting the Server
```bash
cd ./orisun/src/main/orisun
go run .
```

### Client Libraries
- Go client: `orisun-client-go`
- More coming soon...

## Architecture
Orisun uses:
- PostgreSQL for durable event storage and consistency guarantees
- NATS JetStream for real-time event streaming and pub/sub
- gRPC for client-server communication
- Protobuf for efficient serialization

## Performance
- Handles thousands of events per second
- Efficient querying with PostgreSQL indexes
- Load balanced message distribution
- Optimized for both write and read operations

## Contributing
1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License
This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.