# Installing Tinode

## Using Docker

See [instructions](./docker/README.md)

## Building from Source

- Install [Go environment](https://golang.org/doc/install)

- Install [RethinkDB](https://www.rethinkdb.com/docs/install/)

- Fetch, build tinode server and tinode-db database initializer:
 - `go get github.com/tinode/chat/server && go install github.com/tinode/chat/server`
 - `go get github.com/tinode/chat/tinode-db && go install github.com/tinode/chat/tinode-db`

- Download javascript client for testing:
 - https://github.com/tinode/example-react-js/archive/master.zip
 - https://github.com/tinode/tinode-js/archive/master.zip

## Running

- Run RethinkDB:
  `rethinkdb --bind all --daemon`

- Run DB initializer
 `$GOPATH/bin/tinode-db -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf`
 - add `-data=$GOPATH/src/github.com/tinode/chat/tinode-db/data.json` flag if you want sample data to be loaded.

 DB intializer needs to be run only once per installation. See [instructions](tinode-db/README.md) for more options.

- Unpack JS client to a directory, for instance $HOME/tinode/example-react-js/ by first unzipping https://github.com/tinode/example-react-js/archive/master.zip then extract tinode.js from https://github.com/tinode/tinode-js/archive/master.zip to the same directory.

- Run server `$GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$HOME/tinode/example-react-js/`

- Test your installation by pointing your browser to http://localhost:6060/x/example-react-js/
