# Using Docker to run Tinode

All images are available at https://hub.docker.com/r/tinode/

1. [Install Docker](https://docs.docker.com/install/) 1.8 or above. The provided dockerfiles are dependent on [Docker networking](https://docs.docker.com/network/) which may not work with the older Docker.

2. Create a bridge network. It's used to connect Tinode container with the database container.
	```
	$ docker network create tinode-net
	```

3. Decide which database backend you want to use: MySQL, PostgreSQL, MongoDB or RethinkDB. Run the selected database container, attaching it to `tinode-net` network:

	1. **MySQL**: If you've decided to use MySQL backend, run the official MySQL Docker container:
	```
	$ docker run --name mysql --network tinode-net --restart always --env MYSQL_ALLOW_EMPTY_PASSWORD=yes -d mysql:5.7
	```
	See [instructions](https://hub.docker.com/_/mysql/) for more options. MySQL 5.7 or above is required.

	2. **PostgreSQL**: If you've decided to use PostgreSQL backend, run the official PostgreSQL Docker container:
	```
	$ docker run --name postgres --network tinode-net --restart always --env POSTGRES_PASSWORD=postgres -d postgres:13
	```
	See [instructions](https://hub.docker.com/_/postgres/) for more options. PostgresSQL 13 or above is required.

	The name `rethinkdb`, `mysql`, `mongodb` or `postgres` in the `--name` assignment is important. It's used by other containers as a database's host name.

	3. **MongoDB**: If you've decided to use MongoDB backend, run the official MongoDB Docker container and initialise it as single node replica set (you can change "rs0" if you wish):
   ```
   $ docker run --name mongodb --network tinode-net --restart always -d mongo:latest --replSet "rs0"
   $ docker exec -it mongodb mongosh

   # And inside mongo shell:
   > rs.initiate( {"_id": "rs0", "members": [ {"_id": 0, "host": "mongodb:27017"} ]} )
   > quit()
   ```
	See [instructions](https://hub.docker.com/_/mongo/) for more options. MongoDB 4.2 or above is required.

	4. **RethinkDB**: If you've decided to use RethinkDB backend, run the official RethinkDB Docker container:
	```
	$ docker run --name rethinkdb --network tinode-net --restart always -d rethinkdb:2.3
	```
	See [instructions](https://hub.docker.com/_/rethinkdb/) for more options.

4. Run the Tinode container for the appropriate database:

	1. **MySQL**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-mysql:latest
	```

	2. **PostgreSQL**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-postgres:latest
	```

	3. **MongoDB**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-mongodb:latest
	```

	4. **RethinkDB**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-rethinkdb:latest
	```

	You can also run Tinode with the `tinode/tinode` image (which has all of the above DB adapters compiled in). You will need to specify the database adapter via `STORE_USE_ADAPTER` environment variable. E.g. for `mysql`, the command line will look like
	```
	$ docker run -p 6060:6060 -d -e STORE_USE_ADAPTER mysql --name tinode-srv --network tinode-net tinode/tinode:latest
	```

	See [below](#supported-environment-variables) for more options.

	The port mapping `-p 5678:1234` tells Docker to map container's port 1234 to host's port 5678 making server accessible at http://localhost:5678/. The container will initialize the database with test data on the first run.

	You may replace `:latest` with a different tag. See all all available tags here:
	 * [MySQL tags](https://hub.docker.com/r/tinode/tinode-mysql/tags/)
	 * [PostgreSQL tags](https://hub.docker.com/r/tinode/tinode-postgresql/tags/) (beta version)
	 * [MongoDB tags](https://hub.docker.com/r/tinode/tinode-mongodb/tags/)
	 * [RethinkDB tags](https://hub.docker.com/r/tinode/tinode-rethink/tags/)
	 * [All bundle tags](https://hub.docker.com/r/tinode/tinode/tags/)

5. Test the installation by pointing your browser to [http://localhost:6060/](http://localhost:6060/).

## Optional

### External config file

The container comes with a built-in config file which can be customized with values from the environment variables (see [Supported environment variables](#supported_environment_variables) below). If changes are extensive it may be more convenient to replace the built-in config file with a custom one. In that case map the config file located on your host (e.g. `/users/jdoe/new_tinode.conf`) to container (e.g. `/tinode.conf`) using [Docker volumes](https://docs.docker.com/storage/volumes/) `--volume /users/jdoe/new_tinode.conf:/tinode.conf` then instruct the container to use the new config `--env EXT_CONFIG=/tinode.conf`:
```
$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net \
		--volume /users/jdoe/new_tinode.conf:/tinode.conf \
		--env EXT_CONFIG=/tinode.conf \
		tinode/tinode-mysql:latest
```
When `EXT_CONFIG` is set, most other environment variables are ignored. Consult [the table](#supported-environment-variables) below for a full list.


### Resetting or upgrading the database

The database schema may change from time to time. An error `Invalid database version 101. Expected 103` means the schema has changed and needs to be updated, in this case from version 101 to version 103. You need to either reset or upgrade the database to continue:

Shut down the Tinode container and remove it:
```
$ docker stop tinode-srv && docker rm tinode-srv
```
then repeat step 4 adding `--env RESET_DB=true` to reset or `--env UPGRADE_DB=true` to upgrade.

Also, the database is automatically created if missing.


### Enable push notifications

Tinode uses Google Firebase Cloud Messaging (FCM) to send pushes.
Follow [instructions](../docs/faq.md#q-how-to-setup-fcm-push-notifications) for obtaining the required FCM credentials.

* Download and save the [FCM service account credentials](https://cloud.google.com/docs/authentication/production) file.
* Obtain values for `apiKey`, `messagingSenderId`, `projectId`, `appId`, `messagingVapidKey`.
Assuming your Firebase credentials file is named `myproject-1234-firebase-adminsdk-abc12-abcdef012345.json`
and it's saved at `/Users/jdoe/`, web API key is `AIRaNdOmX4ULR-X6ranDomzZ2bHdRanDomq2tbQ`, Sender ID `141421356237`,
Project ID `myproject-1234`, App ID `1:141421356237:web:abc7de1234fab56cd78abc`, VAPID key (a.k.a. "Web Push certificates")
is `83_Or_So_Random_Looking_Characters`, start the container with the following parameters (using MySQL container as an example):

```
$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net \
		-v /Users/jdoe:/config \
		--env FCM_CRED_FILE=/config/myproject-1234-firebase-adminsdk-abc12-abcdef012345.json \
		--env FCM_API_KEY=AIRaNdOmX4ULR-X6ranDomzZ2bHdRanDomq2tbQ \
		--env FCM_APP_ID=1:141421356237:web:abc7de1234fab56cd78abc \
		--env FCM_PROJECT_ID=myproject-1234 \
		--env FCM_SENDER_ID=141421356237 \
		--env FCM_VAPID_KEY=83_Or_So_Random_Looking_Characters \
		tinode/tinode-mysql:latest
```

### Configure video calling

Tinode uses [WebRTC](https://webrtc.org/) for video and audio calls. WebRTC needs [Interactive Communication Establishment (ICE)](https://tools.ietf.org/id/draft-ietf-ice-rfc5245bis-13.html) [TURN(S)](https://en.wikipedia.org/wiki/Traversal_Using_Relays_around_NAT) and/or [STUN](https://en.wikipedia.org/wiki/STUN) servers to traverse [NAT](https://en.wikipedia.org/wiki/Network_address_translation), otherwise calls may not work. Tinode does not include TURN(S) or STUN out of the box. You need to obtain and configure your own service. Once you setup your TURN(S) and/or STUN service, save its configuration to a file, for example `/Users/jdoe/turn-config.json` and provide path to this file when starting the container:

```
$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net \
		-v /Users/jdoe:/config \
		--env ICE_SERVERS_FILE=/config/turn-config.json \
		< ... other config parameters ... >
		tinode/tinode-mysql:latest
```

The config file uses the following format:
```json
[
  {
    "urls": [
      "stun:stun.example.com"
    ]
  },
  {
    "username": "user-name-to-use-for-authentication-with-the-server",
    "credential": "your-password",
    "urls": [
      "turn:turn.example.com:80?transport=udp",
      "turn:turn.example.com:3478?transport=tcp",
      "turns:turn.example.com:443?transport=tcp",
    ]
  }
]
```

[XIRSYS](https://xirsys.com/) offers a free tier for developers. We are in no way affiliated with XIRSYS. We do not endorse or otherwise take any responsibility for your use of their services.

### Run the chatbot

See [instructions](../chatbot/python/).

The chatbot password is generated only when the database is initialized or reset. It's saved to `/botdata` directory in the container. If you want to keep the data available between container changes, such as image upgrades, make sure the `/botdata` is a mounted volume (i.e. you always launch the container with `--volume botdata:/botdata` option).

## Supported environment variables

You can specify the following environment variables when issuing `docker run` command:

| Variable | Type | Default | Purpose |
| --- | --- | --- | --- |
| `ACC_GC_ENABLED`[^2] | bool | `false` | Enable/diable automatic deletion of unfinished account registrations. |
| `AUTH_TOKEN_KEY`[^2] | string | `wfaY2RgF2S1OQI/ZlK+LS​rp1KB2jwAdGAIHQ7JZn+Kc=` | base64-encoded 32 random bytes used as salt for authentication tokens. |
| `AWS_ACCESS_KEY_ID`[^2] | string |  | AWS Access Key ID when using `s3` media handler. |
| `AWS_CORS_ORIGINS`[^2] | string | `["*"]` | Allowed origins ([CORS](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Origin)) URL for downloads. Generally use your server URL and its aliases. |
| `AWS_REGION`[^2] | string |  | AWS Region when using `s3` media handler |
| `AWS_S3_BUCKET`[^2] | string |  | Name of the AWS S3 bucket when using `s3` media handler. |
| `AWS_S3_ENDPOINT`[^2] | string |  | An endpoint URL (hostname only or fully qualified URI) to override the default endpoint; can be of any S3-compatible service, such as `minio-api.x.io` |
| `AWS_SECRET_ACCESS_KEY`[^2] | string |  | AWS [Secret Access Key](https://aws.amazon.com/blogs/security/wheres-my-secret-access-key/) when using `s3` media handler. |
| `CLUSTER_SELF` | string |  | Node name if the server is running in a Tinode cluster. |
| `DEBUG_EMAIL_VERIFICATION_CODE`[^2] | string |  | Enable dummy email verification code, e.g. `123456`. Disabled by default (empty string). |
| `DEFAULT_COUNTRY_CODE`[^2] | string | `US` | 2-letter country code to assign to sessions by default when the country isn't specified by the client explicitly and it's impossible to infer it. |
| `EXT_CONFIG`[^1] | string |  | Path to external config file to use instead of the built-in one. If this parameter is used, most other variables are ignored[^1]. |
| `EXT_STATIC_DIR` | string |  | Path to external directory containing static data (e.g. Tinode Webapp files). |
| `FCM_CRED_FILE`[^2] | string |  | Path to JSON file with FCM server-side service account credentials which will be used to send push notifications. |
| `FCM_API_KEY` | string |  | Firebase API key; required for receiving push notifications in the web client. |
| `FCM_APP_ID` | string |  | Firebase web app ID; required for receiving push notifications in the web client. |
| `FCM_PROJECT_ID` | string |  | Firebase project ID; required for receiving push notifications in the web client. |
| `FCM_SENDER_ID` | string |  | Firebase FCM sender ID; required for receiving push notifications in the web client. |
| `FCM_VAPID_KEY` | string |  | Also called 'Web Client certificate' in the FCM console; required by the web client to receive push notifications. |
| `FCM_INCLUDE_ANDROID_NOTIFICATION`[^2] | boolean | true | If true, pushes a data + notification message, otherwise a data-only message. [More info](https://firebase.google.com/docs/cloud-messaging/concept-options). |
| `FCM_MEASUREMENT_ID` | string |  | Google Analytics ID of the form `G-123ABCD789`. |
| `FS_CORS_ORIGINS`[^2] | string | `["*"]` | Cors origins when media is served from the file system. See `AWS_CORS_ORIGINS` for details. |
| `ICE_SERVERS_FILE`[^2] | string |  | Path to JSON file with configuration of ICE servers to be used for video calls. |
| `MEDIA_HANDLER`[^2] | string | `fs` | Handler of large files, either `fs` or `s3`. |
| `MYSQL_DSN`[^2] | string | <code>'root@tcp(mysql)/tinode?&#8203;parseTime=true&#8203;&collation=utf8mb4_0900_ai_ci'</code> | MySQL [DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name). |
| `PLUGIN_PYTHON_CHAT_BOT_ENABLED`[^2] | bool | `false` | Enable calling into the plugin provided by Python chatbot. |
| `POSTGRES_DSN`[^2] | string | <code>'postgresql://postgres:postgres@&#8203;localhost:5432/tinode?&#8203;sslmode=disable&#8203;&connect_timeout=10'</code> |  PostgreSQL [DSN](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING). |
| `RESET_DB` | bool | `false` | Drop and recreate the database. |
| `SAMPLE_DATA` | string |  _see comment →_ | File with sample data to load. Default `data.json` when resetting or generating new DB, none when upgrading. Use `` (empty string) to disable. |
| `SMTP_AUTH_MECHANISM`[^2] | string | `"plain"` | SMTP authentication mechanism to use; one of "login", "cram-md5", "plain". |
| `SMTP_DOMAINS`[^2] | string |  | White list of email domains; when non-empty, accept registrations with emails from these domains only (email verification). |
| `SMTP_HELO_HOST`[^2] | string | _see comment →_ | FQDN to use in SMTP HELO/EHLO command; if missing, the hostname from `SMTP_HOST_URL` is used. |
| `SMTP_HOST_URL`[^2] | string | `'http://localhost:6060/'` | URL of the host where the webapp is running (email verification). |
| `SMTP_LOGIN`[^2] | string |  | Optional login to use for authentication with the SMTP server (email verification). |
| `SMTP_PASSWORD`[^2] | string |  | Optional password to use for authentication with the SMTP server (email verification). |
| `SMTP_PORT`[^2] | number |  | Port number of the SMTP server to use for sending verification emails, e.g. `25` or `587`. |
| `SMTP_SENDER` | string |  | [RFC 5322](https://tools.ietf.org/html/rfc5322) email address to use in the `FROM` field of verification emails, e.g. `'"John Doe" <jdoe@example.com>'`. |
| `SMTP_SERVER`[^2] | string |  | Name of the SMTP server to use for sending verification emails, e.g. `smtp.gmail.com`. If SMTP_SERVER is not defined, email verification will be disabled. |
| `STORE_USE_ADAPTER`[^2] | string |  | DB adapter name (specify with `tinode/tinode` container only). |
| `TEL_HOST_URL`[^2] | string | `'http://localhost:6060/'` | URL of the host where the webapp is running for phone verification. |
| `TEL_SENDER`[^2] | string |  | Sender name to pass to SMS sending service. |
| `TLS_CONTACT_ADDRESS`[^2] | string |  | Optional email to use as contact for [LetsEncrypt](https://letsencrypt.org/) certificates, e.g. `jdoe@example.com`. |
| `TLS_DOMAIN_NAME`[^2] | string |  | If non-empty, enables TLS (http**s**) and configures domain name of your container, e.g. `www.example.com`. In order for TLS to work you have to expose your HTTPS port to the Internet and correctly configure DNS. It WILL FAIL with `localhost` or unroutable IPs. |
| `TNPG_AUTH_TOKEN` | string |  | Tinode Push Gateway authentication token. |
| `TNPG_ORG`[^2] | string |  | Tinode Push Gateway organization name as registered at https://console.tinode.co |
| `UID_ENCRYPTION_KEY`[^2] | string | `la6YsO+bNX/+XIkOqc5Svw==` | base64-encoded 16 random bytes used as an encryption key for user IDs. |
| `UPGRADE_DB` | bool | `false` | Upgrade database schema, if necessary. |
| `WAIT_FOR` | string |  | If non-empty, waits for the specified database `host:port` to be available before starting the server. |

[^1]: If set, variables marked with the footnote `[2]` are ignored.
[^2]: Ignored if `EXT_CONFIG` is set.

A convenient way to generate a desired number of random bytes and base64-encode them on Linux and Mac:
```
$ openssl rand -base64 <desired length>
```

## Metrics Exporter

See [monitoring/exporter/README](../monitoring/exporter/README.md) for information on the Exporter.
Container is also available as a part of the Tinode docker distribution: `tinode/exporter`.
Run it with

```
$ docker run -p 6222:6222 -d --name tinode-exporter --network tinode-net \
		--env SERVE_FOR=<prometheus|influxdb> \
		--env TINODE_ADDR=<tinode metrics endpoint> \
		... <monitoring service specific vars> \
		tinode/exporter:latest
```

Available variables:

| Variable | Type | Default | Function |
| --- | --- | --- | --- |
| `SERVE_FOR` | string | `` | Monitoring service: `prometheus` or `influxdb` |
| `TINODE_ADDR` | string | `http://localhost/stats/expvar/` | Tinode metrics path |
| `INFLUXDB_VERSION` | string | `1.7` | InfluxDB version (`1.7` or `2.0`) |
| `INFLUXDB_ORGANIZATION` | string | `org` | InfluxDB organization |
| `INFLUXDB_PUSH_INTERVAL` | int | `60` | Exporter metrics push interval in seconds |
| `INFLUXDB_PUSH_ADDRESS` | string | `https://mon.tinode.co/intake` | InfluxDB backend url |
| `INFLUXDB_AUTH_TOKEN` | string | `` | InfluxDB auth token |
| `PROM_NAMESPACE` | string | `tinode` | Prometheus namespace |
| `PROM_METRICS_PATH` | string | `/metrics` | Exporter webserver path that Prometheus server scrapes |
