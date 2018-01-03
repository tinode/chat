#!/bin/bash
python -m grpc_tools.protoc -I../pbx --python_out=../pbx --grpc_python_out=../pbx ../pbx/model.proto
