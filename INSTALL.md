# Installing Tinode

## Using Docker

Coming soon.

## Building from Source

- Install [Go environment](https://golang.org/doc/install)
- Install [RethinkDB](https://www.rethinkdb.com/docs/install/)
- Fetch, build tinode server and tinode-db database initializer
 - `go get github.com/tinode/chat/server && go install github.com/tinode/chat/server`
 - `go get github.com/tinode/chat/tinode-db && go get github.com/tinode/chat/tinode-db`
- Copy config files to appropriate location (usually `$GOPATH/bin/`):
  - `cp github.com/tinode/chat/tinode-db/config $GOPATH/bin/config-tinode-db`
  - `cp github.com/tinode/chat/server/config $GOPATH/bin/config-server`
- If necessary, copy javascript and html files, as well as sample data:
  - `cp github.com/tinode/chat/server/static $GOPATH/bin/`
- Run RethinkDB:
 `$ rethinkdb --bind all --daemon`
- Run DB initializer
 `$ tinode-db --config=config-tinode-db`
 - add `--data=data.json` flag if you want sample data to be loaded
- Run server `$ $GOPATH/bin/server --config=config-server`
- Check if everything is running properly by opening http://localhost:6060/x/chatdemo.html
