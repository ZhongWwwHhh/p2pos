#!/bin/bash

# Build script with version injection
# Usage: ./build.sh [output_name]

set -e

# Get version from current date/time in yyyymmdd-hhmm format
VERSION=$(date +%Y%m%d-%H%M)

# Output file name
OUTPUT="${1:-p2pos}"

# Get the OS and architecture
OS="${GOOS:-$(go env GOOS)}"
ARCH="${GOARCH:-$(go env GOARCH)}"

# Add extension for Windows
if [ "$OS" = "windows" ]; then
    OUTPUT="${OUTPUT}.exe"
fi

echo "Building P2POS..."
echo "Version: $VERSION"
echo "OS: $OS"
echo "Arch: $ARCH"
echo "Output: $OUTPUT"

# Build with version injection via ldflags
go build \
    -ldflags "-X p2pos/internal/config.AppVersion=$VERSION" \
    -o "$OUTPUT" \
    main.go

echo "Build complete: $OUTPUT"
