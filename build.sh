#!/bin/bash


TARGET_OS="darwin" # Change to "darwin" for macOS or "windows" for Windows
TARGET_ARCH="arm64" # Change to "386" for 32-bit

# Set the output binary name and the target OS/architecture
OUTPUT_NAME="orisun-$TARGET_OS-$TARGET_ARCH"

# Build the binary
echo "Building for $TARGET_OS/$TARGET_ARCH..."
GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -o $OUTPUT_NAME ./src/orisun/main/main.go

# Check if the build was successful
if [ $? -eq 0 ]; then
    echo "Build successful! Binary created: ./$OUTPUT_NAME"
else
    echo "Build failed!"
fi