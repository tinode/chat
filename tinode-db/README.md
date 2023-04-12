# Utility to initialize or upgrade `tinode` DB

This utility initializes the `tinode` database (or upgrades an existing DB from an earlier version) and optionally loads it with data. To force database reset use command line option `--reset=true`.

## Build the package:

 - **RethinkDB**
  `go build -tags rethinkdb` or `go build -i -tags rethinkdb` to automatically install missing dependencies.

 - **MySQL**
  `go build -tags mysql` or `go build -i -tags mysql` to automatically install missing dependencies.

 - **MongoDB**
  `go build -tags mongodb` or `go build -i -tags mongodb` to automatically install missing dependencies.

 - **PostgreSQL**
  `go build -tags postgres` or `go build -i -tags postgres` to automatically install missing dependencies.


## Run

Run from the command line.

`tinode-db [parameters]`

Command line parameters:
 - `--reset`: delete the database then re-create it in a blank state; it has no effect if the database does not exist.
 - `--upgrade`: upgrade database from an earlier version retaining all the data; make sure to backup the DB before upgrading.
 - `--no_init`: check that database exists but don't create it if missing.
 - `--data=FILENAME`: fill `tinode` database with data from the provided file. See [data.json](data.json).
 - `--config=FILENAME`: load configuration from FILENAME. Example config is included as [tinode.conf](tinode.conf).
 - `--make_root=USER_ID`: promote an existing user to root user, `USER_ID` of the form `usrAbCDef123`.
 - `--add_root=USERNAME[:PASSWORD]`: create a new user account and make it root; if password is missing, a strong password will be generated.

Configuration file options:
 - `uid_key` is a base64-encoded 16 byte XTEA encryption key to (weakly) encrypt object IDs so they don't appear sequential. You probably want to use your own key in production.
 - `store_config.adapters.mysql` and `store_config.adapters.rethinkdb` are database-specific sections:
  - `database` is the name of the database to generate.
  - `addresses` is RethinkDB/MongoDB's host and port number to connect to. An array of hosts can be provided as well `["host1", "host2"]`.
  - `dsn` is MySQL's Data Source Name.
  - `replica_set` is MongoDB's Replicaset name.

The `uid_key` is only used if the sample data is being loaded. It should match the key of a production server and should be kept private.

The default `data.json` file creates six users with user names `alice`, `bob`, `carol`, `dave`, `frank`, and `tino` (chat bot user). Passwords are the same as the user names with 123 appended, e.g. user `alice` gets password `alice123`; `tino` gets a randomly generated password. It also creates three group topics, and multiple peer to peer topics. Users are subscribed to topics and to each other. All topics are randomly filled with messages.

Avatar photos curtesy of https://www.pexels.com/ under [CC0 license](https://www.pexels.com/photo-license/).

## Links:

* [RethinkDB schema](https://github.com/tinode/chat/tree/master/server/db/rethinkdb/schema.md)
* [MySQL schema](https://github.com/tinode/chat/tree/master/server/db/mysql/schema.sql)
* [MongoDB schema](https://github.com/tinode/chat/tree/master/server/db/mongodb/schema.md)
* [PostgreSQL schema](https://github.com/tinode/chat/tree/master/server/db/postgres/schema.sql)
