#!/bin/bash

echo "Packaging python tinode-grpc..."

pushd ./pbx > /dev/null

# Generate grpc bindings from the proto file.
./generate-python.sh v=3

pushd ../py_grpc > /dev/null

# Generate version file from git tags
python3 version.py

# Generate tinode-grpc package
python3 setup.py -q sdist bdist_wheel

popd > /dev/null
popd > /dev/null
