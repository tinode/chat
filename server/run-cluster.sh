#!/bin/bash

# Start/stop test cluster on localhost. This is NOT a production script. Use it for reference only.

# Names of cluster nodes
ALL_NODE_NAMES=( one two three )
# Port where the first node will listen for client connections over http
HTTP_BASE_PORT=6080
# Port where the first node will listen for gRPC intra-cluster connections.
GRPC_BASE_PORT=16060

# Assign command line parameters to variables.
# for line in $@; do
#  eval "$line"
#done

while [[ $# -gt 0 ]]; do
  key="$1"
  shift
  echo "$key"
  case "$key" in
    -c|--config)
      config=$1
      shift # value
      ;;
    start)
      if [ ! -z "$config" ] ; then
        TINODE_CONF=$config
      else
        TINODE_CONF="tinode.conf"
      fi

      echo "HTTP ports 6080-6082, gRPC ports 16060-16062, config ${config}"

      HTTP_PORT=$HTTP_BASE_PORT
      GRPC_PORT=$GRPC_BASE_PORT
      for NODE_NAME in "${ALL_NODE_NAMES[@]}"
      do
        # Start the node
        ./server -config=${TINODE_CONF} -cluster_self=${NODE_NAME} -listen=:${HTTP_PORT} -grpc_listen=:${GRPC_PORT} &
        # Save PID of the node to a temp file.
        # /var/tmp/ does not requre root access.
        echo $!> "/var/tmp/tinode-${NODE_NAME}.pid"
        # Increment ports for the next node.
        HTTP_PORT=$((HTTP_PORT+1))
        GRPC_PORT=$((GRPC_PORT+1))
      done
      exit 0
      ;;
    stop)
      echo 'Stopping cluster'

      for NODE_NAME in "${ALL_NODE_NAMES[@]}"
      do
        # Read PIDs of running nodes from temp files and kill them.
        kill `cat /var/tmp/tinode-${NODE_NAME}.pid`
        # Clean up: delete temp files.
        rm "/var/tmp/tinode-${NODE_NAME}.pid"
      done
      exit 0
      ;;
    *)
      echo $"Usage: $0 {start|stop} [ --config <path_to_tinode.conf> ]"
      exit 1
  esac
done
