#!/bin/bash

# Build Tinode docker images

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

# Build an images for various DB backends
for dbtag in "${dbtags[@]}"
do
  rmitags="tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}.${ver[2]}"
  buildtags="--tag tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}.${ver[2]}"
  if [ -n "$FULLRELEASE" ]; then
    rmitags="${rmitags} tinode/tinode-${dbtag}:latest tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}"
    buildtags="${buildtags} --tag tinode/tinode-${dbtag}:latest --tag tinode/tinode-${dbtag}:${ver[0]}.${ver[1]}"
  fi
  docker rmi ${rmitags}
  docker build --build-arg VERSION=$tag --build-arg TARGET_DB=${dbtag} ${buildtags} docker/tinode
done

# Build chatbot image
buildtags="--tag tinode/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
rmitags="tinode/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
if [ -n "$FULLRELEASE" ]; then
  rmitags="${rmitags} tinode/chatbot:latest tinode/chatbot:${ver[0]}.${ver[1]}"
  buildtags="${buildtags}  --tag tinode/chatbot:latest --tag tinode/chatbot:${ver[0]}.${ver[1]}"
fi
docker rmi ${rmitags}
docker build --build-arg VERSION=$tag ${buildtags} docker/chatbot
