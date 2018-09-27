#!/bin/bash

# This generates python gRPC bindings for Tinode.
python -m grpc_tools.protoc -I../pbx --python_out=../py_grpc/tinode_grpc --grpc_python_out=../py_grpc/tinode_grpc ../pbx/model.proto
# Bindings are incompatible with Python packaging system. This is a fix.
python py_fix.py
