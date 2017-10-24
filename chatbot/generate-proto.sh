#!/bin/bash
python -m grpc_tools.protoc -I../plugin --python_out=. --grpc_python_out=. ../plugin/model.proto
