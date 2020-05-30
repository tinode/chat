#!/bin/bash

# Publish MidnightChat docker images

function containerName() {
  if [ "$1" == "alldbs" ]; then
    # For alldbs, container name is simply MidnightChat.
    local name="MidnightChat"
  else
    # Otherwise, MidnightChat-$dbtag.
    local name="MidnightChat-${dbtag}"
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
    docker push MidnightChat/${name}:latest
    docker push MidnightChat/${name}:"${ver[0]}.${ver[1]}"
  fi
  docker push MidnightChat/${name}:"${ver[0]}.${ver[1]}.${ver[2]}"
done

# Deploy chatbot images
if [ -n "$FULLRELEASE" ]; then
  docker push MidnightChat/chatbot:latest
  docker push MidnightChat/chatbot:"${ver[0]}.${ver[1]}"
fi
docker push MidnightChat/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"

# Deploy exporter images
if [ -n "$FULLRELEASE" ]; then
  docker push MidnightChat/exporter:latest
  docker push MidnightChat/exporter:"${ver[0]}.${ver[1]}"
fi
docker push MidnightChat/exporter:"${ver[0]}.${ver[1]}.${ver[2]}"

docker logout
