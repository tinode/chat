#!/bin/bash
python -m grpc_tools.protoc -I../pbx --python_out=../py_grpc/tinode_grpc --grpc_python_out=../py_grpc/tinode_grpc ../pbx/model.proto
