#!/bin/bash

# Cross-compiling script using https://github.com/mitchellh/gox
# I use this to compile the Linux version of the server on my Mac.

# Supported OSs: darwin windows linux
goplat=( darwin windows linux )
# Supported CPU architectures: amd64
goarc=( amd64 )
# Supported database tags
dbtags=( mysql rethinkdb )

pushd ${GOHOME}chat > /dev/null

# Get last git tag as release version. Tag looks like 'v.1.2.3', so strip 'v'.
version=`git tag | tail -1`
version=${version#?}

# Prepare directory for the new release
rm -fR ./releases/${version}
mkdir ./releases/${version}

for plat in "${goplat[@]}"
do
  for arc in "${goarc[@]}"
  do
    # Keygen is database-independent
    # Remove previous build
    rm -f $GOPATH/bin/keygen
    # Build
    ~/go/bin/gox -osarch="${plat}/${arc}" -output $GOPATH/bin/keygen ./keygen > /dev/null

    for dbtag in "${dbtags[@]}"
    do
      echo "Building ${dbtag}-${plat}/${arc}..."

      # Remove previous builds
      rm -f $GOPATH/bin/tinode
      rm -f $GOPATH/bin/init-db
      # Build tinode server and database initializer for RethinkDb and MySQL.
      ~/go/bin/gox -osarch="${plat}/${arc}" \
        -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'` -X main.builtfordb=${dbtag}" \
        -tags ${dbtag} -output $GOPATH/bin/tinode ./server > /dev/null
      ~/go/bin/gox -osarch="${plat}/${arc}" -tags ${dbtag} -output $GOPATH/bin/init-db ./tinode-db > /dev/null
      # Tar on Mac is inflexible about directories. Let's just copy release files to
      # one directory.
      rm -fR ./releases/tmp
      mkdir -p ./releases/tmp/static

      # Copy templates and database initialization files
      cp ./server/tinode.conf ./releases/tmp
      cp -R ./server/templ ./releases/tmp
      cp -R ./server/static/img ./releases/tmp/static
      cp -R ./server/static/audio ./releases/tmp/static
      cp -R ./server/static/css ./releases/tmp/static
      cp ./server/static/index.html ./releases/tmp/static
      cp ./server/static/tinode.js ./releases/tmp/static
      cp ./server/static/drafty.js ./releases/tmp/static
      cp ./tinode-db/data.json ./releases/tmp
      cp ./tinode-db/*.jpg ./releases/tmp
      cp ./tinode-db/credentials.sh ./releases/tmp

      # Build archive. All platforms but Windows use tar for archiving. Windows uses zip.
      if [ "$plat" = "windows" ]; then
        # Copy binaries
        cp $GOPATH/bin/tinode.exe ./releases/tmp
        cp $GOPATH/bin/init-db.exe ./releases/tmp
        cp $GOPATH/bin/keygen.exe ./releases/tmp

        # Remove possibly existing archive.
        rm -f ./releases/${version}/tinode-${dbtag}."${plat}-${arc}".zip
        # Generate a new one
        pushd ./releases/tmp > /dev/null
        zip -q -r ../${version}/tinode-${dbtag}."${plat}-${arc}".zip ./*
        popd > /dev/null
      else
        # Copy binaries
        cp $GOPATH/bin/tinode ./releases/tmp
        cp $GOPATH/bin/init-db ./releases/tmp
        cp $GOPATH/bin/keygen ./releases/tmp

        # Remove possibly existing archive.
        rm -f ./releases/${version}/tinode-${dbtag}."${plat}-${arc}".tar.gz
        # Generate a new one
        tar -C ${GOHOME}chat/releases/tmp -zcf ./releases/${version}/tinode-${dbtag}."${plat}-${arc}".tar.gz .
      fi
    done
  done
done

# Clean up temporary files
rm -fR ./releases/tmp

popd > /dev/null
