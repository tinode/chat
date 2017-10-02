# Installing Tinode

## Using Docker

See [instructions](./docker/README.md)

## Building from Source

- Install [Go environment](https://golang.org/doc/install)

- Install [RethinkDB](https://www.rethinkdb.com/docs/install/)

- Fetch, build tinode server and tinode-db database initializer:
 - `go get github.com/tinode/chat/server && go install -tags rethinkdb github.com/tinode/chat/server`
 - `go get github.com/tinode/chat/tinode-db && go install -tags rethinkdb github.com/tinode/chat/tinode-db`

Note the required `-tags rethinkdb` build option. You may also optionally define `main.buildstamp` for the server by adding a build option
```
-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`
```
This build timestamp will be sent by the server to the clients.

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

- Rename tinode.conf-example to tinode.conf.

- Run server `$GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$HOME/tinode/example-react-js/`

- Test your installation by pointing your browser to http://localhost:6060/x/. Keep in mind that by default the static files from the `-static_data` path are served at `/x/`. You can change this by editing the line `static_mount` in the config file.

-  If you want to use an [Android client](https://github.com/tinode/android-example) and want push notification to work, find the section `"push"` in `tinode.conf`, item `"name": "fcm"`, then change `"disabled"` to `false`. Go to https://console.firebase.google.com/ (https://console.firebase.google.com/project/**NAME-OF-YOUR-PROJECT**/settings/cloudmessaging) and get a server key. Paste the key to the `"api_key"` field. See more at [https://github.com/tinode/android-example].

### Note on running the server in background

There is [no clean way](https://github.com/golang/go/issues/227) to daemonize a Go process internally. One must use external tools such as shell `&` operator, `systemd`, `launchd`, `SMF`, `daemon tools`, `runit`, etc. to run the process in the background.

Specific note for `nohup` users: an `exit` must be issued immediately after `nohup` call to close the foreground session cleanly:

```
> nohup $GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$HOME/tinode/example-react-js/ &
> exit
```

Otherwise `SIGHUP` may be received by the server if the shell connection is broken before the ssh session has terminated (indicated by `Connection to XXX.XXX.XXX.XXX port 22: Broken pipe`). In such a case the server will shutdown because `SIGHUP` is intercepted by the server and interpreted as a shutdown request.

For more details see https://github.com/tinode/chat/issues/25.
