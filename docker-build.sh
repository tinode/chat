#!/bin/bash

# Build MidnightChat docker images

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

# if version contains a dash, it's not a full releave, i.e. v0.1.15.5-rc1
if [[ ${ver[2]} != *"-"* ]]; then
  FULLRELEASE=1
fi

dbtags=( mysql mongodb rethinkdb alldbs )

# Build an images for various DB backends
for dbtag in "${dbtags[@]}"
do
  if [ "$dbtag" == "alldbs" ]; then
    # For alldbs, container name is MidnightChat/MidnightChat.
    name="MidnightChat/MidnightChat"
  else
    # Otherwise, MidnightChat/MidnightChat-$dbtag.
    name="MidnightChat/MidnightChat-${dbtag}"
  fi
  separator=
  rmitags="${name}:${ver[0]}.${ver[1]}.${ver[2]}"
  buildtags="--tag ${name}:${ver[0]}.${ver[1]}.${ver[2]}"
  if [ -n "$FULLRELEASE" ]; then
    rmitags="${rmitags} ${name}:latest ${name}:${ver[0]}.${ver[1]}"
    buildtags="${buildtags} --tag ${name}:latest --tag ${name}:${ver[0]}.${ver[1]}"
  fi
  docker rmi ${rmitags}
  docker build --build-arg VERSION=$tag --build-arg TARGET_DB=${dbtag} ${buildtags} docker/MidnightChat
done

# Build chatbot image
buildtags="--tag MidnightChat/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
rmitags="MidnightChat/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
if [ -n "$FULLRELEASE" ]; then
  rmitags="${rmitags} MidnightChat/chatbot:latest MidnightChat/chatbot:${ver[0]}.${ver[1]}"
  buildtags="${buildtags}  --tag MidnightChat/chatbot:latest --tag MidnightChat/chatbot:${ver[0]}.${ver[1]}"
fi
docker rmi ${rmitags}
docker build --build-arg VERSION=$tag ${buildtags} docker/chatbot

# Build exporter image
buildtags="--tag MidnightChat/exporter:${ver[0]}.${ver[1]}.${ver[2]}"
rmitags="MidnightChat/exporter:${ver[0]}.${ver[1]}.${ver[2]}"
if [ -n "$FULLRELEASE" ]; then
  rmitags="${rmitags} MidnightChat/exporter:latest MidnightChat/exporter:${ver[0]}.${ver[1]}"
  buildtags="${buildtags}  --tag MidnightChat/exporter:latest --tag MidnightChat/exporter:${ver[0]}.${ver[1]}"
fi
docker rmi ${rmitags}
docker build --build-arg VERSION=$tag ${buildtags} docker/exporter
