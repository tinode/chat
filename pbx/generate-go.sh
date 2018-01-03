#!/bin/bash
go generate protoc --proto_path=../pbx --go_out=plugins=grpc:../pbx ../pbx/model.proto
