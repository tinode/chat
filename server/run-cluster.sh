#!/bin/bash

# Start/stop test cluster on localhost. This is NOT a production script. Use it for reference only.

# Names of cluster nodes
node_names=( one two three )
# Port where the first node will listen for client connections over http
base_http_port=6080
# Port where the first node will listen for gRPC connections.
base_grpc_port=6090

case "$1" in
  start)
    echo 'Running cluster on localhost, ports 6080-6082'

    http_port=$base_http_port
    grpc_port=$base_grpc_port
    for node_name in "${node_names[@]}"
    do
      ./server -config=./tinode.conf -cluster_self=$node_name -listen=:${http_port} -grpc_listen=:${grpc_port} &
      # /var/tmp/ does not requre root access
      echo $!> "/var/tmp/tinode-${node_name}.pid"
      http_port=$((http_port+1))
      grpc_port=$((grpc_port+1))
    done
    ;;
  stop)
    echo 'Stopping cluster'

    for node_name in "${node_names[@]}"
    do
      kill `cat /var/tmp/tinode-${node_name}.pid`
      rm "/var/tmp/tinode-${node_name}.pid"
    done
    ;;
  *)
    echo $"Usage: $0 {start|stop}"
esac
