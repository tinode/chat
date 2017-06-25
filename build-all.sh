#!/bin/bash

~/go/bin/gox -osarch="linux/amd64" -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`" ./server
~/go/bin/gox -osarch="linux/amd64" ./tinode-db
~/go/bin/gox -osarch="linux/amd64" ./keygen
