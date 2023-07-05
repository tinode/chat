/******************************************************************************
 *
 *  Description :
 *
 *  Server entrypoint.
 *
 *****************************************************************************/

package main

//go:generate protoc --go_out=../pbx --go_opt=paths=source_relative --go-grpc_out=../pbx --go-grpc_opt=paths=source_relative ../pbx/model.proto

import (
	"github.com/tinode/chat/server"
)

func main() {
  a := &server.App{}
  a.Run()
}
