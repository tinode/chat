# Using Docker to run Tinode

All images are available at https://hub.docker.com/r/tinode/

1. [Install Docker](https://docs.docker.com/install/) 1.8 or above. The provided dockerfiles are dependent on [Docker networking](https://docs.docker.com/network/) which may not work with the older Docker.

2. Create a bridge network. It's used to connect Tinode container with the database container.
	```
	$ docker network create tinode-net
	```

3. Decide which database backend you want to use: RethinkDB, MySQL or MongoDB. Run the selected database container, attaching it to `tinode-net` network:

	1. **RethinkDB**: If you've decided to use RethinkDB backend, run the official RethinkDB Docker container:
	```
	$ docker run --name rethinkdb --network tinode-net --restart always -d rethinkdb:2.3
	```
	See [instructions](https://hub.docker.com/_/rethinkdb/) for more options.

	2. **MySQL**: If you've decided to use MySQL backend, run the official MySQL Docker container:
	```
	$ docker run --name mysql --network tinode-net --restart always --env MYSQL_ALLOW_EMPTY_PASSWORD=yes -d mysql:5.7
	```
	See [instructions](https://hub.docker.com/_/mysql/) for more options. MySQL 5.7 or above is required.

	3. **MongoDB**: If you've decided to use MongoDB backend, run the official MongoDB Docker container and initialise it as single node replica set (you can change "rs0" if you wish):
   ```
   $ docker run --name mongodb --network tinode-net --restart always -d mongo:latest --replSet "rs0"
   $ docker exec -it mongodb mongo

   # And inside mongo shell:
   > rs.initiate( {"_id": "rs0", "members": [ {"_id": 0, "host": "mongodb:27017"} ]} )
   > quit()
   ```
	See [instructions](https://hub.docker.com/_/mongo/) for more options. MongoDB 4.2 or above is required.

	The name `rethinkdb`, `mysql` or `mongodb` in the `--name` assignment is important. It's used by other containers as a database's host name.

4. Run the Tinode container for the appropriate database:

	1. **RethinkDB**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-rethinkdb:latest
	```

	2. **MySQL**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-mysql:latest
	```

	3. **MongoDB**:
	```
	$ docker run -p 6060:6060 -d --name tinode-srv --network tinode-net tinode/tinode-mongodb:latest
	```

	You can also run Tinode with the `tinode/tinode` image (which has all of the above DB adapters compiled in). You will need to specify the database adapter via `STORE_USE_ADAPTER` environment variable. E.g. for `mysql`, the command line will look like
	```
	$ docker run -p 6060:6060 -d -e STORE_USE_ADAPTER mysql --name tinode-srv --network tinode-net tinode/tinode:latest
	```

	See [below](#supported-environment-variables) for more options.

	The port mapping `-p 5678:1234` tells Docker to map container's port 1234 to host's port 5678 making server accessible at http://localhost:5678/. The container will initialize the database with test data on the first run.

	You may replace `:latest` with a different tag. See all all available tags here:
	 * [MySQL tags](https://hub.docker.com/r/tinode/tinode-mysql/tags/)
	 * [RethinkDB tags](https://hub.docker.com/r/tinode/tinode-rethink/tags/)
	 * [MongoDB tags](https://hub.docker.com/r/tinode/tinode-mongodb/tags/)
	 * [All bundle tags](https://hub.docker.com/r/tinode/tinode/tags/) (comming soon)

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
If you set `EXT_CONFIG` all other environment variables except `RESET_DB`, `FCM_SENDER_ID`, `FCM_VAPID_KEY` are ignored.

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
		-v /Users/jdoe:/fcm \
		--env FCM_CRED_FILE=/fcm/myproject-1234-firebase-adminsdk-abc12-abcdef012345.json \
		--env FCM_API_KEY=AIRaNdOmX4ULR-X6ranDomzZ2bHdRanDomq2tbQ \
		--env FCM_APP_ID=1:141421356237:web:abc7de1234fab56cd78abc \
		--env FCM_PROJECT_ID=myproject-1234 \
		--env FCM_SENDER_ID=141421356237 \
		--env FCM_VAPID_KEY=83_Or_So_Random_Looking_Characters \
		tinode/tinode-mysql:latest
```

### Run the chatbot

See [instructions](../chatbot/python/).

The chatbot password is generated only when the database is initialized or reset. It's saved to `/botdata` directory in the container. If you want to keep the data available between container changes, such as image upgrades, make sure the `/botdata` is a mounted volume (i.e. you always launch the container with `--volume botdata:/botdata` option).

## Supported environment variables

You can specify the following environment variables when issuing `docker run` command:

| Variable | Type | Default | Function |
| --- | --- | --- | --- |
| `AUTH_TOKEN_KEY` | string | `wfaY2RgF2S1OQI/ZlK+LS​rp1KB2jwAdGAIHQ7JZn+Kc=` | base64-encoded 32 random bytes used as salt for authentication tokens. |
| `AWS_ACCESS_KEY_ID` | string |  | AWS Access Key ID when using `s3` media handler |
| `AWS_CORS_ORIGINS` | string | `["*"]` | Allowed origins ([CORS](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Origin)) URL for downloads. Generally use your server URL and its aliases. |
| `AWS_REGION` | string |  | AWS Region when using `s3` media handler |
| `AWS_S3_BUCKET` | string |  | Name of the AWS S3 bucket when using `s3` media handler |
| `AWS_SECRET_ACCESS_KEY` | string |  | AWS [Secret Access Key](https://aws.amazon.com/blogs/security/wheres-my-secret-access-key/) when using `s3` media handler |
| `CLUSTER_SELF` | string |  | Node name if the server is running in a Tinode cluster |
| `DEBUG_EMAIL_VERIFICATION_CODE` | string |  | Enable dummy email verification code, e.g. `123456`. Disabled by default (empty string). |
| `EXT_CONFIG` | string |  | Path to external config file to use instead of the built-in one. If this parameter is used all other variables except `RESET_DB`, `FCM_SENDER_ID`, `FCM_VAPID_KEY` are ignored. |
| `EXT_STATIC_DIR` | string |  | Path to external directory containing static data (e.g. Tinode Webapp files) |
| `FCM_CRED_FILE` | string |  | Path to json file with FCM server-side service account credentials which will be used to send push notifications. |
| `FCM_API_KEY` | string |  | Firebase API key; required for receiving push notifications in the web client |
| `FCM_APP_ID` | string |  | Firebase web app ID; required for receiving push notifications in the web client |
| `FCM_PROJECT_ID` | string |  | Firebase project ID; required for receiving push notifications in the web client |
| `FCM_SENDER_ID` | string |  | Firebase FCM sender ID; required for receiving push notifications in the web client |
| `FCM_VAPID_KEY` | string |  | Also called 'Web Client certificate' in the FCM console; required by the web client to receive push notifications. |
| `FCM_INCLUDE_ANDROID_NOTIFICATION` | boolean | true | If true, pushes a data + notification message, otherwise a data-only message. [More info](https://firebase.google.com/docs/cloud-messaging/concept-options). |
| `MEDIA_HANDLER` | string | `fs` | Handler of large files, either `fs` or `s3` |
| `MYSQL_DSN` | string | `'root@tcp(mysql)/tinode'` | MySQL [DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name). |
| `PLUGIN_PYTHON_CHAT_BOT_ENABLED` | bool | `false` | Enable calling into the plugin provided by Python chatbot |
| `RESET_DB` | bool | `false` | Drop and recreate the database. |
| `SAMPLE_DATA` | string |  _see comment_ | File with sample data to load. Default `data.json` when resetting or generating new DB, none when upgrading. Use `` (empty string) to disable |
| `SMTP_DOMAINS` | string |  | White list of email domains; when non-empty, accept registrations with emails from these domains only (email verification). |
| `SMTP_HOST_URL` | string | `'http://localhost:6060/'` | URL of the host where the webapp is running (email verification). |
| `SMTP_LOGIN` | string |  | Optional login to use for authentication with the SMTP server (email verification). If login is missing, `addr-spec` part of `SMTP_SENDER` will be used: e.g. if `SMTP_SENDER` is `'"John Doe" <jdoe@example.com>'`, `jdoe@example.com` will be used as login. |
| `SMTP_PASSWORD` | string |  | Password to use for authentication with the SMTP server (email verification). |
| `SMTP_PORT` | number |  | Port number of the SMTP server to use for sending verification emails, e.g. `25` or `587`. |
| `SMTP_SENDER` | string |  | [RFC 5322](https://tools.ietf.org/html/rfc5322) email address to use in the `FROM` field of verification emails and for authentication with the SMTP server, e.g. `'"John Doe" <jdoe@example.com>'`. |
| `SMTP_SERVER` | string |  | Name of the SMTP server to use for sending verification emails, e.g. `smtp.gmail.com`. If SMTP_SERVER is not defined, email verification will be disabled. |
| `STORE_USE_ADAPTER` | string |  | DB adapter name (specify with `tinode/tinode` container only) |
| `TLS_CONTACT_ADDRESS` | string |  | Optional email to use as contact for [LetsEncrypt](https://letsencrypt.org/) certificates, e.g. `jdoe@example.com`. |
| `TLS_DOMAIN_NAME` | string |  | If non-empty, enables TLS (http**s**) and configures domain name of your container, e.g. `www.example.com`. In order for TLS to work you have to expose your HTTPS port to the Internet and correctly configure DNS. It WILL FAIL with `localhost` or unroutable IPs. |
| `UID_ENCRYPTION_KEY` | string | `la6YsO+bNX/+XIkOqc5Svw==` | base64-encoded 16 random bytes used as an encryption key for user IDs. |
| `UPGRADE_DB` | bool | `false` | Upgrade database schema, if necessary. |

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
