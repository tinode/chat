# Installing Tinode

## Using Docker

See [instructions](./docker/README.md)

## Building from Source

- Install [Go environment](https://golang.org/doc/install). Make sure Go version is at least 1.9. Building with Go 1.8 or below **will fail**!

- Install [RethinkDB](https://www.rethinkdb.com/docs/install/)

- Fetch, build tinode server and tinode-db database initializer:
 - `go get -tags rethinkdb github.com/tinode/chat/server && go install -tags rethinkdb github.com/tinode/chat/server`
 - `go get -tags rethinkdb github.com/tinode/chat/tinode-db && go install -tags rethinkdb github.com/tinode/chat/tinode-db`

Note the required `-tags rethinkdb` build option. 

You may also optionally define `main.buildstamp` for the server by adding a build option
```
-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`
```
This build timestamp will be sent by the server to the clients.

- Download javascript client for testing:
 - https://github.com/tinode/example-react-js/archive/master.zip
 - https://github.com/tinode/tinode-js/archive/master.zip

## Running standalone server

1. Run RethinkDB:
  `rethinkdb --bind all --daemon`

2. Rename `tinode.conf-example` to `tinode.conf`.

3. Run DB initializer
 `$GOPATH/bin/tinode-db -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf`
 - add `-data=$GOPATH/src/github.com/tinode/chat/tinode-db/data.json` flag if you want sample data to be loaded. 
 - DB intializer needs to be run only once per installation. See [instructions](tinode-db/README.md) for more options.

4. Unpack JS client to a directory, for instance $HOME/tinode/example-react-js/ by first unzipping https://github.com/tinode/example-react-js/archive/master.zip then extract tinode.js from https://github.com/tinode/tinode-js/archive/master.zip to the same directory.

5. Run server `$GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$HOME/tinode/example-react-js/`

6. Test your installation by pointing your browser to http://localhost:6060/x/. Keep in mind that by default the static files from the `-static_data` path are served at `/x/`. You can change this by editing the line `static_mount` in the config file.

7.  If you want to use an [Android client](https://github.com/tinode/android-example) and want push notification to work, find the section `"push"` in `tinode.conf`, item `"name": "fcm"`, then change `"disabled"` to `false`. Go to https://console.firebase.google.com/ (https://console.firebase.google.com/project/**NAME-OF-YOUR-PROJECT**/settings/cloudmessaging) and get a server key. Paste the key to the `"api_key"` field. See more at [https://github.com/tinode/android-example].

## Running a cluster

- Install RethinkDB, run it stanalone or in [cluster mode](https://www.rethinkdb.com/docs/start-a-server/#a-rethinkdb-cluster-using-multiple-machines). Run DB initializer, unpack JS files as described in the previous section.

- Cluster expects at least two nodes. A minimum of three nodes is recommended. A sample cluster config file is provided as `cluster.conf`. The following section configures the cluster.

```
	"cluster_config": {
		"nodes": [
			{"name": "one", "addr":"localhost:12001"},
			{"name": "two", "addr":"localhost:12002"},
			{"name": "three", "addr":"localhost:12003"}
		],
		"self": "one",
		"failover": {
			"enabled": true,
			"heartbeat": 100,
			"vote_after": 8,
			"node_fail_after": 16
		}
	}
```
* `nodes` defines individual cluster nodes. The sample defines three nodes named `one`, `two`, and `tree` running at the localhost at the specified cluster communication ports. Cluster addresses don't need to be exposed to the clients.
* `self` is the name of the current node. Generally it's more convenient to specify the name of the current node at the command line using `cluster_self` option. Command line value overrides the config file value.
* `failover` is an experimental feature which migrates topics from failed cluster nodes keeping them accessible:
  * `enabled` turns on failover mode; failover mode requires at least three nodes in the cluster.
  * `heartbeat` interval in milliseconds between heartbeats sent by the leader node to follower nodes to ensure they are accessible.
  * `vote_after` number of failed heartbeats before a new leader node is elected.
  * `node_fail_after` number of heartbeats that a follower node misses before it's cosidered to be down.

If you are testing the cluster with all nodes running on the same host, you also must override the `listen` port. Here is an example for launching two cluster nodes from the same host using the same config file:
```
./server -config=./cluster.conf -static_data=./example-react-js/ -listen=:6060 -cluster_self=one &
./server -config=./cluster.conf -static_data=./example-react-js/ -listen=:6061 -cluster_self=two &
```

### Note on running the server in background

There is [no clean way](https://github.com/golang/go/issues/227) to daemonize a Go process internally. One must use external tools such as shell `&` operator, `systemd`, `launchd`, `SMF`, `daemon tools`, `runit`, etc. to run the process in the background.

Specific note for [nohup](https://en.wikipedia.org/wiki/Nohup) users: an `exit` must be issued immediately after `nohup` call to close the foreground session cleanly:

```
> nohup $GOPATH/bin/server -config=$GOPATH/src/github.com/tinode/chat/server/tinode.conf -static_data=$HOME/tinode/example-react-js/ &
> exit
```

Otherwise `SIGHUP` may be received by the server if the shell connection is broken before the ssh session has terminated (indicated by `Connection to XXX.XXX.XXX.XXX port 22: Broken pipe`). In such a case the server will shutdown because `SIGHUP` is intercepted by the server and interpreted as a shutdown request.

For more details see https://github.com/tinode/chat/issues/25.

# Using MySQL Storage (experimental)

1. Follow instructions for building from sources replacing all occurences of `-tags rethinkdb` with `-tags mysql`.

2. Make sure MySQL (or MariaDB or Percona) is installed and running. The code has been tested with MySQL 5.7 but it should work with earlier versions as well.

3. Rename `tinode.conf-example` to `tinode.conf`

4. Open `tinode.conf`. In the section `"store_config"` find `"use_adapter": "rethinkdb"` and replace it with `"use_adapter": "mysql"`. Check that the [DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name) in `"mysql"` section is appropriate for your MySQL installation. Option `parseTime=true` is required. 
```js
	"mysql": {
		"dsn": "root@tcp(localhost)/tinode?parseTime=true",
		"database": "tinode"
	},
```

5. Follow the instructions for running as a standalone server from step 3.