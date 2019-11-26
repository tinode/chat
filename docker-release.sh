#!/bin/bash

# Publish Tinode docker images

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

dbtags=( mysql mongodb rethinkdb )

# Read dockerhub login/password from a separate file
source .dockerhub

# Obtain dockerhub API auth token
jstoken=`curl -X POST \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"${user}\",\"password\":\"${pass}\"}" \
  https://hub.docker.com/v2/users/login/ \
  | python -c "import json,sys;obj=json.load(sys.stdin);print obj['token'];"`

# Remove earlier builds
for dbtag in "${dbtags[@]}"
do
  if [ -n "$FULLRELEASE" ]; then
    curl -i -X DELETE \
      -H "Accept: application/json" \
      -H "Authorization: JWT ${jstoken}" \
      https://hub.docker.com/v2/repositories/tinode/tinode-${dbtag}/tags/latest/

    curl -i -X DELETE \
      -H "Accept: application/json" \
      -H "Authorization: JWT ${jstoken}" \
      https://hub.docker.com/v2/repositories/tinode/tinode-${dbtag}/tags/${ver[0]}.${ver[1]}/
  fi
  curl -i -X DELETE \
    -H "Accept: application/json" \
    -H "Authorization: JWT ${jstoken}" \
    https://hub.docker.com/v2/repositories/tinode/tinode-${dbtag}/tags/${ver[0]}.${ver[1]}.${ver[2]}/
done

if [ -n "$FULLRELEASE" ]; then
  curl -i -X DELETE \
    -H "Accept: application/json" \
    -H "Authorization: JWT ${jstoken}" \
    https://hub.docker.com/v2/repositories/tinode/chatbot/tags/latest/
  curl -i -X DELETE \
    -H "Accept: application/json" \
    -H "Authorization: JWT ${jstoken}" \
    https://hub.docker.com/v2/repositories/tinode/chatbot/tags/${ver[0]}.${ver[1]}/
fi
curl -i -X DELETE \
  -H "Accept: application/json" \
  -H "Authorization: JWT ${jstoken}" \
  https://hub.docker.com/v2/repositories/tinode/chatbot/tags/${ver[0]}.${ver[1]}.${ver[2]}/

# Deploy images for various DB backends
for dbtag in "${dbtags[@]}"
do
  # Deploy tagged image
  if [ -n "$FULLRELEASE" ]; then
    docker push tinode/tinode-${dbtag}:latest
    docker push tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}"
  fi
  docker push tinode/tinode-${dbtag}:"${ver[0]}.${ver[1]}.${ver[2]}"
done

# Deploy chatbot images
if [ -n "$FULLRELEASE" ]; then
  docker push tinode/chatbot:latest
  docker push tinode/chatbot:"${ver[0]}.${ver[1]}"
fi
docker push tinode/chatbot:"${ver[0]}.${ver[1]}.${ver[2]}"
