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

if [[ ${ver[2]} != *"-rc"* ]]; then
  FULLRELEASE=1
fi

dbtags=( mysql rethinkdb )

# Remove earlier builds
for dbtag in "${dbtags[@]}"
do
  if [ FULLRELEASE = 1 ]; then
    docker rmi -f tinode/tinode-${dbtag}:latest
    docker rmi -f tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}"
  fi
  docker rmi -f tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}.${ver[2]}"
done

if [ FULLRELEASE = 1 ]; then
  docker rmi tinode/chatbot:latest
  docker rmi tinode/chatbot:"${ver[0]}.${ver[1]}"
fi
docker rmi tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"

# Build an images for various DB backends
for dbtag in "${dbtags[@]}"
do
  buildtags="--tag tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}.${ver[2]}"
  if [ FULLRELEASE = 1 ]; then
    buildtags="${buildtags} --tag tinode/tinode-${dbtag}:latest --tag tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}"
  fi
  docker build --build-arg VERSION=$tag --build-arg TARGET_DB=${dbtag} ${buildtags} docker/tinode

  # Deploy tagged image
  if [ FULLRELEASE = 1 ]; then
    docker push tinode/tinode-${dbtag}:latest
    docker push tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}"
  fi
  docker push tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}.${ver[2]}"
done

# Build chatbot image
buildtags="--tag tinode/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
if [ FULLRELEASE = 1 ]; then
  buildtags="${buildtags}  --tag tinode/chatbot:latest --tag tinode/chatbot:${ver[0]}.${ver[1]}"
fi
docker build --build-arg VERSION=$tag ${buildtags} docker/chatbot

# Deploy tagged images
if [ FULLRELEASE = 1 ]; then
  docker push tinode/chatbot:latest
  docker push tinode/chatbot:"${ver[0]}.${ver[1]}"
fi
docker push tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"
