#!/bin/bash
set -e

echo "Generating Protobuf code..."
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/hsagent.proto

echo "Protobuf code generated successfully!"

ls -la proto/*.pb.go