#!/bin/bash

# This scripts build and archives binaries and supporting files.

# Supported OSs: mac (darwin), windows, linux.
goplat=( darwin darwin windows linux )

# CPUs architectures: amd64 and arm64. The same order as OSs.
goarc=( amd64 arm64 amd64 amd64 )

# Number of platform+architectures.
buildCount=${#goplat[@]}

for line in $@; do
  eval "$line"
done

# Strip 'v' prefix as in v0.16.4 -> 0.16.4.
version=${tag#?}

if [ -z "$version" ]; then
  # Get last git tag as release version. Tag looks like 'v.1.2.3', so strip 'v'.
  version=`git describe --tags`
  version=${version#?}
fi

echo "Releasing exporter $version"

GOSRC=${GOPATH}/src/github.com/tinode

pushd ${GOSRC}/chat > /dev/null

# Make sure earlier builds are deleted.
rm -f ./releases/${version}/exporter*

for (( i=0; i<${buildCount}; i++ ));
do
  plat="${goplat[$i]}"
  arc="${goarc[$i]}"

  echo "Building ${plat}/${arc}..."

  # Remove possibly existing binaries from earlier builds.
  rm -f ./releases/tmp/exporter*

  # Environment to cros-compile for the platform.
  env GOOS="${plat}" GOARCH="${arc}" go build \
    -ldflags "-s -w -X main.buildstamp=`git describe --tags`" \
    -o ./releases/tmp/exporter ./monitoring/exporter > /dev/null

  # Build archive. All platforms but Windows use tar for archiving. Windows uses zip.
  if [ "$plat" = "windows" ]; then
    # Just copy the binary with .exe appended.
    cp ./releases/tmp/exporter ./releases/${version}/exporter."${plat}-${arc}".exe
  else
    plat2=$plat
    # Rename 'darwin' tp 'mac'
    if [ "$plat" = "darwin" ]; then
      plat2=mac
    fi

    # Just copy the binary.
    cp ./releases/tmp/exporter ./releases/${version}/exporter."${plat2}-${arc}"
  fi

done

popd > /dev/null
