# Command Line Client for Tinode

This is a command line chat client. It's written in Python as a demonstration of gRPC Tinode API.

Files generated from [protobuf](../pbx/model.proto) are provided for convenience. Files are generated with command
```
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```
