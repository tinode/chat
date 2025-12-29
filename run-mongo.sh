#!/bin/bash

if [ "$1" = "rs" ]; then
    echo "Starting replicaset rs0"
    mongod --dbpath /usr/local/var/mongodb/  --replSet "rs0"
    exit 0
fi
echo "Starting standalone"
mongod --dbpath /usr/local/var/mongodb/
