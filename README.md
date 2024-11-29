# Orisun - A Batteries Included Event Store

## Description
Orisun is a robust event sourcing solution built on PostgreSQL and NATS JetStream. It provides a reliable, scalable event store with built-in pub/sub capabilities, making it ideal for event-driven architectures and CQRS applications.

### Key Features
- **Dynamic Consistency Boundaries (DCB)**: Unlike traditional event stores that use streams as consistency boundaries, Orisun uses the DCB concept inspired by Axon Framework
- **Global Ordering**: Built-in global ordering guarantee for events
- **Optimistic Concurrency**: Prevents conflicts while allowing parallel event processing
- **Real-time Event Streaming**: Subscribe to event changes in real-time
- **Load Balanced Pub/Sub**: Distribute messages across multiple consumers
- **Flexible Event Querying**: Query events by various criteria including custom tags
- **High Performance**: Efficient PostgreSQL-based storage with NATS JetStream for streaming

## Prerequisites
- PostgreSQL 13+
- NATS Server 2.9+ with JetStream enabled
- Go 1.20+

## Installation

1. Clone the repository:
```bash
git clone https://github.com/yourusername/orisun.git
cd orisun
```

2. Install dependencies:
```bash
go mod download
```

3. Set up the database:
```bash
psql -U your_user -d your_database -f scripts/schema.sql
```

## Configuration

Create a `config.yaml` file:
```yaml
db:
  host: localhost
  port: 5432
  user: postgres
  password: your_password
  name: your_database
  schema: your_schema

nats:
  url: nats://localhost:4222

server:
  port: 50051
```

## Usage

### Starting the Server
```bash
go run cmd/orisun/main.go -config path/to/config.yaml
```

### gRPC API

#### SaveEvents
Save events with optimistic concurrency control:
```protobuf
rpc SaveEvents(SaveEventsRequest) returns (WriteResult)
```

Example request:
```json
{
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
    "events": [
        {
            "event_id": "evt-123",
            "event_type": "UserCreated",
            "tags": [
                {"key": "aggregate_id", "value": "user-123"},
                {"key": "version", "value": "1"}
            ],
            "data": "{\"username\": \"john_doe\"}",
            "metadata": "{\"user_agent\": \"mozilla\"}"
        }
    ],
    "boundary": "users"
}
```

#### GetEvents
Query events with flexible criteria:
```protobuf
rpc GetEvents(GetEventsRequest) returns (GetEventsResponse)
```

#### SubscribeToEvents
Subscribe to real-time event updates:
```protobuf
rpc SubscribeToEvents(SubscribeToEventStoreRequest) returns (stream Event)
```

#### Pub/Sub
```protobuf
rpc PublishToPubSub(PublishRequest) returns (google.protobuf.Empty)
rpc SubscribeToPubSub(SubscribeRequest) returns (stream SubscribeResponse)
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

## Support
For support, please open an issue in the GitHub repository or contact the maintainers.