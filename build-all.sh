#!/bin/bash

~/go/bin/gox -osarch="linux/amd64" -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`" -tags rethinkdb ./server
~/go/bin/gox -osarch="linux/amd64" -tags rethinkdb ./tinode-db
~/go/bin/gox -osarch="linux/amd64" ./keygen
