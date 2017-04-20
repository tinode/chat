# Using Docker files to fun Tinode

[Install Docker](http://docs.docker.com/engine/installation/).

Run the official RethinkDB Docker container:

```
$ docker run --name rethinkdb -d rethinkdb
```
The name `rethinkdb` in the name assignment `--name rethinkdb` is important. It's used by subsequent containers to get RethinkDB's host address and port numbers.

Build the initializer image from the Dockerfile provided:
```
$ docker build --tag=tinode-init-db init-rethinkdb
```

Run the container to initialize the `tinode` database:
```
$ docker run --rm --name tinode-init --link rethinkdb tinode-init-db

```
Optionally you may want to provide a UID encryption key as `--env SNOWFLAKE_UID_KEY=base64+encoded+16+bytes=`. The system uses [snowflake](https://github.com/tinode/snowflake) to generate unique IDs for values like user IDs. To make them unpredictable it encrypts them with [XTEA](https://en.wikipedia.org/wiki/XTEA). If you don't provide the key the system will use a default one. As a result the IDs will be easily predictable (but still not sequential).

At this point the database is initialized and loaded with test data. No need to do this again unless you need to resent the data or delete/recreated the RethinkDB container.

Build the Tinode server image from the Dockerfile:
```
$ docker build --tag=tinode-srv tinode-server
```

Run Tinode server:
```
$ docker run  -p 6060:18080 -d --name tinode-srv --link rethinkdb tinode-srv

```
The port mapping `-p 6060:18080` tells Docker to map container's port 18080 to host's port 6060 making server accessible at http://localhost:6060/. The sample application is located at http://localhost:6060/x/example-react-js/index.html.

If you provided a `SNOWFLAKE_UID_KEY` during initialization, you must provide the same key at this step too. Otherwise primary key collisions may happen.
