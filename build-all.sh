#!/bin/bash

go install -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`" ./server
go install ./tinode-db
go install ./keygen
