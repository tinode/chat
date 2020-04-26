#!/bin/bash

# Publish Tinode docker images

function containerName() {
  if [ "$1" == "alldbs" ]; then
    # For alldbs, container name is simply tinode.
    local name="tinode"
  else
    # Otherwise, tinode-$dbtag.
    local name="tinode-${dbtag}"
  fi
  echo $name
}

for line in $@; do
  eval "$line"
done

tag=${tag#?}

if [ -z "$tag" ]; then
    echo "Must provide tag as 'tag=v1.2.3' or 'v1.2.3-abc0'"
    exit 1
fi

# Convert tag into a version
ver=( ${tag//./ } )

if [[ ${ver[2]} != *"-"* ]]; then
  FULLRELEASE=1
fi

dbtags=( mysql mongodb rethinkdb alldbs )

# Read dockerhub login/password from a separate file
source .dockerhub

# Login to docker hub
docker login -u $user -p $pass

# Deploy images for various DB backends
for dbtag in "${dbtags[@]}"
do
  name="$(containerName $dbtag)"
  # Deploy tagged image
  if [ -n "$FULLRELEASE" ]; then
    docker push tinode/${name}:latest
    docker push tinode/${name}:"${ver[0]}.${ver[1]}"
  fi
  docker push tinode/${name}:"${ver[0]}.${ver[1]}.${ver[2]}"
done

# Deploy chatbot images
if [ -n "$FULLRELEASE" ]; then
  docker push tinode/chatbot:latest
  docker push tinode/chatbot:"${ver[0]}.${ver[1]}"
fi
docker push tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"

# Deploy exporter images
if [ -n "$FULLRELEASE" ]; then
  docker push tinode/exporter:latest
  docker push tinode/exporter:"${ver[0]}.${ver[1]}"
fi
docker push tinode/exporter:"${ver[0]}.${ver[1]}.${ver[2]}"

docker logout
