#!/bin/bash

# Build Tinode docker linux/amd64 images.
# You may have to install buildx https://docs.docker.com/buildx/working-with-buildx/
# if your build host and target architectures are different (e.g. building on a Mac
# with Apple silicon).

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

# Use buildx if the current platform is not x86.
buildcmd='build'
if [ `uname -m` != 'x86_64' ]; then
  buildcmd='buildx build --platform=linux/amd64'
fi

dbtags=( mysql postgres mongodb rethinkdb alldbs )

# Build an images for various DB backends
for dbtag in "${dbtags[@]}"
do
  if [ "$dbtag" == "alldbs" ]; then
    # For alldbs, container name is tinode/tinode.
    name="tinode/tinode"
  else
    # Otherwise, tinode/tinode-$dbtag.
    name="tinode/tinode-${dbtag}"
  fi
  separator=
  rmitags="${name}:${ver[0]}.${ver[1]}.${ver[2]}"
  buildtags="--tag ${name}:${ver[0]}.${ver[1]}.${ver[2]}"
  if [ -n "$FULLRELEASE" ]; then
    rmitags="${rmitags} ${name}:latest ${name}:${ver[0]}.${ver[1]}"
    buildtags="${buildtags} --tag ${name}:latest --tag ${name}:${ver[0]}.${ver[1]}"
  fi
  docker rmi ${rmitags}
  docker ${buildcmd} --build-arg VERSION=$tag --build-arg TARGET_DB=${dbtag} ${buildtags} docker/tinode
done

# Build chatbot image
buildtags="--tag tinode/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
rmitags="tinode/chatbot:${ver[0]}.${ver[1]}.${ver[2]}"
if [ -n "$FULLRELEASE" ]; then
  rmitags="${rmitags} tinode/chatbot:latest tinode/chatbot:${ver[0]}.${ver[1]}"
  buildtags="${buildtags}  --tag tinode/chatbot:latest --tag tinode/chatbot:${ver[0]}.${ver[1]}"
fi
docker rmi ${rmitags}
docker ${buildcmd} --build-arg VERSION=$tag ${buildtags} docker/chatbot

# Build exporter image
buildtags="--tag tinode/exporter:${ver[0]}.${ver[1]}.${ver[2]}"
rmitags="tinode/exporter:${ver[0]}.${ver[1]}.${ver[2]}"
if [ -n "$FULLRELEASE" ]; then
  rmitags="${rmitags} tinode/exporter:latest tinode/exporter:${ver[0]}.${ver[1]}"
  buildtags="${buildtags}  --tag tinode/exporter:latest --tag tinode/exporter:${ver[0]}.${ver[1]}"
fi
docker rmi ${rmitags}
docker ${buildcmd} --build-arg VERSION=$tag ${buildtags} docker/exporter
