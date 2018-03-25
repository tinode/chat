#!/bin/bash

# Start test cluster on one host. This is provided just as an example.

./server -config=./tinode.conf -cluster_self=one -listen=:6060 &
./server -config=./tinode.conf -cluster_self=two -listen=:6061 &
./server -config=./tinode.conf -cluster_self=three -listen=:6062 &
