# Installing Tinode

## Using Docker

See [instructions](./docker/README.md)

## Building from Source

- Install [Go environment](https://golang.org/doc/install)

- Install [RethinkDB](https://www.rethinkdb.com/docs/install/)

- Fetch, build tinode server and tinode-db database initializer:
 - `go get github.com/tinode/chat/server && go install github.com/tinode/chat/server`
 - `go get github.com/tinode/chat/tinode-db && go install github.com/tinode/chat/tinode-db`

## Running

- Run RethinkDB:
  `rethinkdb --bind all --daemon`

- Run DB initializer
 `$GOPATH/bin/tinode-db -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf`
 - add `-data=$GOPATH/src/github.com/tinode/chat/tinode-db/data.json` flag if you want sample data to be loaded.

 DB intializer needs to be run only once per installation. See [instructions](tinode-db/README.md) for more options.

- Run server `$GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$GOPATH/src/github.com/tinode/chat/server/static`

- Check if everything is running properly by opening [http://localhost:6060/x/samples/chatdemo.html](http://localhost:6060/x/samples/chatdemo.html)
