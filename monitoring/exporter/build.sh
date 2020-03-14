#!/bin/bash

# Cross-compiling script using https://github.com/mitchellh/gox
# This scripts build and archives binaries and supporting files.

# Check if gox is installed. Abort otherwise.
command -v gox >/dev/null 2>&1 || {
  echo >&2 "This script requires https://github.com/mitchellh/gox. Please install it before running."; exit 1;
}

# Supported OSs: darwin windows linux
goplat=( darwin windows linux )
# Supported CPU architectures: amd64
goarc=( amd64 )

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

# Make sure earlier build is deleted
rm -f ./releases/${version}/exporter*

for plat in "${goplat[@]}"
do
  for arc in "${goarc[@]}"
  do
    # Remove previous build
    rm -f $GOPATH/bin/exporter
    # Build
    gox -osarch="${plat}/${arc}" \
      -ldflags "-s -w -X main.buildstamp=`git describe --tags`" \
      -output $GOPATH/bin/exporter ./monitoring/exporter > /dev/null

      # Copy binary to release folder for pushing to Github.
      if [ "$plat" = "windows" ]; then
        # Copy binaries
        cp $GOPATH/bin/exporter.exe ./releases/${version}/exporter."${plat}-${arc}".exe
      else
        plat2=$plat
        # Rename 'darwin' tp 'mac'
        if [ "$plat" = "darwin" ]; then
          plat2=mac
        fi
        # Copy binaries
        cp $GOPATH/bin/exporter ./releases/${version}/exporter."${plat2}-${arc}"
      fi

  done
done

popd > /dev/null
