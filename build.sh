#!/bin/bash

# 构建脚本

set -e

echo "Building gameserver..."

mkdir -p bin

echo "Building center..."
go build -o bin/center ./cmd/center

echo "Building login..."
go build -o bin/login ./cmd/login

echo "Building gateway..."
go build -o bin/gateway ./cmd/gateway

echo "Building game..."
go build -o bin/game ./cmd/game

echo "Building rank..."
go build -o bin/rank ./cmd/rank

echo "All binaries built successfully!"
ls -lh bin/