#!/bin/bash

# Build and publish Tinode docker images

for line in $@; do
  eval "$line"
done

tag=${tag#?}

if [ -z "$tag" ]; then
    echo "Must provide tag as 'tag=v1.2.3'"
    exit 1
fi

# Convert tag into a version
ver=( ${tag//./ } )

# Remove earlier builds
docker rmi tinode-rethinkdb
docker rmi tinode/tinode-rethinkdb:latest
docker rmi tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}"
docker rmi tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}"
docker rmi tinode-mysql
docker rmi tinode/tinode-mysql:latest
docker rmi tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}"
docker rmi tinode/tinode-mysql:"${ver[0]}.${ver[1]}"
docker rmi tino-chatbot
docker rmi tinode/chatbot:latest
docker rmi tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"
docker rmi tinode/chatbot:"${ver[0]}.${ver[1]}"

# Build an image for RethinkDB
docker build --build-arg TARGET_DB=rethinkdb --tag tinode-rethinkdb \
  --tag tinode/tinode-rethinkdb:latest \
  --tag tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}" \
  --tag tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}" docker/tinode

# Deploy tagged image
docker push tinode/tinode-rethinkdb:latest
docker push tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}"
docker push tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}"

# Build an image for MySQL.
docker build --build-arg TARGET_DB=mysql --tag tinode-mysql \
  --tag tinode/tinode-mysql:latest \
  --tag tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}" \
  --tag tinode/tinode-mysql:"${ver[0]}.${ver[1]}" docker/tinode

docker push tinode/tinode-mysql:latest
docker push tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}"
docker push tinode/tinode-mysql:"${ver[0]}.${ver[1]}"

# Build chatbot image
docker build --tag tino-chatbot \
  --tag tinode/chatbot:latest \
  --tag tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}" \
  --tag tinode/chatbot:"${ver[0]}.${ver[1]}" docker/chatbot

# Deploy tagged images
docker push tinode/chatbot:latest
docker push tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"
docker push tinode/chatbot:"${ver[0]}.${ver[1]}"
