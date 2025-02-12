#!/bin/bash

# Default to current system's OS and architecture if not specified
TARGET_OS=${1:-"darwin"}
TARGET_ARCH=${2:-"arm64"}

# Set the output binary name and the target OS/architecture
OUTPUT_NAME="orisun-$TARGET_OS-$TARGET_ARCH"

# Build the binary
echo "Building for $TARGET_OS/$TARGET_ARCH..."
GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -o ./build/$OUTPUT_NAME ./src/orisun/main/main.go

# Check if the build was successful
if [ $? -eq 0 ]; then
    echo "Build successful! Binary created: ./build/$OUTPUT_NAME"
else
    echo "Build failed!"
fi