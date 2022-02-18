#!/bin/bash

# Generate python gRPC bindings for Tinode. A command line parameter v=XX will use specified python version,
# i.e. ./generate-python.sh v=3 will use python3.

for line in $@; do
  eval "$line"
done

python="python${v}"

# This generates python gRPC bindings for Tinode.
$python -m grpc_tools.protoc -I../pbx --python_out=../py_grpc/tinode_grpc --grpc_python_out=../py_grpc/tinode_grpc ../pbx/model.proto
# Bindings are incompatible with Python packaging system. This is a fix.
$python py_fix.py
