# Orisun Event Store

## Description
Orisun Event Store is a robust event sourcing solution built on PostgreSQL. It allows you to store and retrieve events efficiently, making it ideal for applications that require event-driven architecture.

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

```

## Contributing
Contributions are welcome! Please open an issue or submit a pull request for any enhancements or bug fixes.

## License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.