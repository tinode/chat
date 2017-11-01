# Utility to Create `tinode` DB in a local RethinkDB Cluster

Build the package:

`go build -tags rethinkdb` or `go build -i -tags rethinkdb` to automatically install missing dependencies.

Then run from the command line.

`tinode-db [parameters]`

Parameters:
 - `--reset`: delete `tinode` database if one exists, then re-create it in a blank state;
 - `--data=FILENAME`: fill `tinode` database with sample data from the provided file
 - `--config=FILENAME`: load configuration from FILENAME. Example config:
```js
{
	"store_config": {
		"adapter": "rethinkdb",
		"worker_id": 1,
		"uid_key": "la6YsO+bNX/+XIkOqc5Svw==",
		"adapter_config": {
			"database": "tinode",
			"addresses": "localhost:28015"
		}
	}
}
 ```
 
RethinkDB adapter uses [snowflake](http://github.com/tinode/snowflake/) to generate object IDs. The `worker_id` and `uid_key` parameters are used to initialize snowflake and only used when sample data is loaded.
  - `worker_id` is the snowflake ID of the host running this utility, integer in the range 0 - 1023
  - `uid_key` is a base64-encoded 16 byte XTEA encryption key to (weakly) encrypt snowflake-generated IDs so they don't appear sequential. You probably want to use your own key in production.
  - `adapter_config.database` is the name of database to generate
  - `adapter_config.addresses` is RethinkDB's host and port number to connect to. An array of hosts can be provided as well `["host1", "host2"]`

The `uid_key` and `worker_id` are only used if the sample data is being loaded. In such a case they should match those of a production server (and uid_key should be kept private), otherwise uniqueness of object keys is not guaranteed.

The default `data.json` file creates six users with user names `alice`, `bob`, `carol`, `dave`, `frank` and `tino` (used as a chat bot example). Passwords are the same as the user names with 123 appended, e.g. user `alice` gets password `alice123`; `tino` gets a randomly generated password. It also creates three group topics, and multiple peer to peer topics. Users will be subscribed to topics and each other. All topics will be randomly filled with messages.

Avatar photos curtesy of https://www.pexels.com/ under [CC0 license](https://www.pexels.com/photo-license/).

## Links:

See [database schema](https://github.com/tinode/chat/tree/master/server/dbschema.md)
