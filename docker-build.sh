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
docker rmi tinode/tinode-rethinkdb:latest
docker rmi tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}"
docker rmi tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}"
docker rmi tinode/tinode-mysql:latest
docker rmi tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}"
docker rmi tinode/tinode-mysql:"${ver[0]}.${ver[1]}"

# Build a docker image
docker build --build-arg TARGET_DB=rethinkdb --tag tinode-rethinkdb \
  --tag tinode/tinode-rethinkdb:latest \
  --tag tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}" \
  --tag tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}" docker/tinode

# Deploy tagged images
docker push tinode/tinode-rethinkdb:latest
docker push tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}.${ver[2]}"
docker push tinode/tinode-rethinkdb:"${ver[0]}.${ver[1]}"


docker build --build-arg TARGET_DB=mysql --tag tinode-mysql \
  --tag tinode/tinode-mysql:latest \
  --tag tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}" \
  --tag tinode/tinode-mysql:"${ver[0]}.${ver[1]}" docker/tinode

docker push tinode/tinode-mysql:latest
docker push tinode/tinode-mysql:"${ver[0]}.${ver[1]}.${ver[2]}"
docker push tinode/tinode-mysql:"${ver[0]}.${ver[1]}"
