#!/bin/bash

# Cross-compiling script using https://github.com/mitchellh/gox
# I use this to compile the Linux version of the server on my Mac.

~/go/bin/gox -osarch="linux/amd64" -ldflags "-X main.buildstamp=rdb.`date -u '+%Y%m%dT%H:%M:%SZ'`" -tags rethinkdb ./server
~/go/bin/gox -osarch="linux/amd64" -tags rethinkdb ./tinode-db
~/go/bin/gox -osarch="linux/amd64" ./keygen
