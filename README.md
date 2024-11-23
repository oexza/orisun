# Orisun (A batteries included Event store.)

## Description
Orisun Event Store is a robust event sourcing solution built on PostgreSQL and Nats. It allows you to store and retrieve events efficiently, making it ideal for applications that require event sourcing and event-driven architecture. Orisun does not use the concept of a stream as the unit of consistency, instead it uses the Dynamic Consistency Boundary (DCB) concept inpired by Axon Framework, enabled by global ordering guarantee built into the event store.

## Installation
To install the Orisun Event Store, clone the repository and install the necessary dependencies.

## Usage
To run the Orisun Event Store, use the following command:

```
You can interact with the event store through the gRPC endpoints defined in the protobuf file.

## gRPC Endpoints
- **SaveEvents**: Save a batch of events.
- **GetEvents**: Retrieve events based on criteria.
- **SubscribeToEvents**: Subscribe to a stream of events.
- **PublishToPubSub**: Publish a message to a Pub/Sub topic.
- **SubscribeToPubSub**: Subscribe to messages from a Pub/Sub topic.

### Example gRPC Client Usage

Here’s a simple example of how to use the gRPC client to save events:

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
                        {"key": "aggregate", "value": "User-1234"}
                    ]
                }
            ]
        }
    },
    "events": [
        {
            "event_id": "0191b93c-5f3c-75c8-92ce-5a3300709178",
            "event_type": "UserCreated",
            "tags": [
                {"key": "aggregate", "value": "Newaaa"},
                {"key": "mamaa", "value": "mia"}
            ],
            "data": "{\"username\": \"orisun\"}",
            "metadata": "{\"id\": \"1234\"}"
        }
    ]
}

```

## Contributing
Contributions are welcome! Please open an issue or submit a pull request for any enhancements or bug fixes.

## License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.