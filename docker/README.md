# Using Docker to run Tinode

All images are available at https://hub.docker.com/r/tinode/

1. [Install Docker](https://docs.docker.com/install/) 1.8 or above. The provided dockerfiles are dependent on [Docker networking](https://docs.docker.com/network/) which may not work with the older Docker.

2. Create a bridge network. It's used to connect Tinode container with the database container.
	```
	$ docker network create tinode-net
	```

3. Decide which database backend you want to use: RethinkDB or MySQL. Run the selected database container, attaching it to `tinode-net` network:

	1. **RethinkDB**: If you've decided to use RethinkDB backend, run the official RethinkDB Docker container:
	```
	$ docker run --name rethinkdb --network tinode-net -d rethinkdb:2.3
	```
	See [instructions](https://hub.docker.com/_/rethinkdb/) for more options.

	2. **MySQL**: If you've decided to use MySQL backend, run the official MySQL Docker container:
	```
	$ docker run --name mysql --network tinode-net --env MYSQL_ALLOW_EMPTY_PASSWORD=yes -d mysql:5.7
	```
	See [instructions](https://hub.docker.com/_/mysql/) for more options.

	The name `rethinkdb` or `mysql` in the `--name` assignment is important. It's used by other containers as a database's host name.

4. Run the Tinode container for the appropriate database:

	1. **RethinkDB**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --network tinode-net tinode/tinode-rethinkdb:latest
	```

	2. **MySQL**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --network tinode-net tinode/tinode-mysql:latest
	```

	See [below](#supported-environment-variables) for more options.

	The port mapping `-p 6060:18080` tells Docker to map container's port 18080 to host's port 6060 making server accessible at http://localhost:6060/. The container will initialize the database with test data on the first run.

	You may replace `:latest` with a different tag. See all all available tags here:
	 * [MySQL tags](https://hub.docker.com/r/tinode/tinode-mysql/tags/)
	 * [RethinkDB tags](https://hub.docker.com/r/tinode/tinode-rethink/tags/)

5. Test the installation by pointing your browser to [http://localhost:6060/](http://localhost:6060/).

## Optional

### Resetting the database

The data in the database is reset when either one of the following conditions is true:

* File `/botdata/.tn-cookie` is missing.
* `RESET_DB` environment variable is true.

If you want to keep the data in the database between image upgrades, make sure the `/botdata` is a mounted volume (i.e. you launch the container with `--volume botdata:/botdata` option).

If you want to reset the data in the database regardless of `/botdata/.tn-cookie` presence, shut down the Tinode container and remove it:
```
$ docker stop tinode-srv && docker rm tinode-srv
```
then repeat step 4 adding `--env RESET_DB=true`.

### Enable push notifications

Download and save the file with [FCM service account credentials](https://cloud.google.com/docs/authentication/production).
Assuming your Firebase project is `myproject-1234`, credentials file is named `myproject-1234-firebase-adminsdk-abc12-abcdef012345.json` and it's saved at `/Users/jdoe/`, start the container with the following parameters (using MySQL container as an example):

```
$ docker run -p 6060:18080 -d --name tinode-srv --network tinode-net \
		-v /Users/jdoe:/fcm \
		--env FCM_CRED_FILE=/fcm/myproject-1234-firebase-adminsdk-abc12-abcdef012345.json \
		--env FCM_PROJECT_ID=myproject-1234 tinode/tinode-mysql:latest
```

### Run the chatbot

See [instructions](../chatbot/).

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
| `DEBUG_EMAIL_VERIFICATION_CODE` | string |  | Enable dummy email verification code, e.g. `123456`. Disabled by default (empty string). |
| `FCM_PROJECT_ID` | string |  | FCM project name. Used for push notifications. |
| `FCM_CRED_FILE` | string |  | Path to json file with FCM service account credentials which will be used to send push notifications. |
| `MEDIA_HANDLER` | string | `fs` | Handler of large files, either `fs` or `s3` |
| `MYSQL_DSN` | string | `'root@tcp(mysql)/tinode'` | MySQL [DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name). |
| `RESET_DB` | bool | `false` | Drop and recreate the database. |
| `SMTP_HOST_URL` | string | `'http://localhost:6060/'` | URL of the host where the webapp is running (email verification). |
| `SMTP_PASSWORD` | string |  | Password to use for authentication with the SMTP server (email verification). |
| `SMTP_PORT` | number |  | Port number of the SMTP server to use for sending verification emails, e.g. `25` or `587`. |
| `SMTP_SENDER` | string |  | [RFC 5322](https://tools.ietf.org/html/rfc5322) email address to use in the `FROM` field of verification emails and for authentication with the SMTP server, e.g. `'"John Doe" <jdoe@example.com>'`. |
| `SMTP_SERVER` | string |  | Name of the SMTP server to use for sending verification emails, e.g. `smtp.gmail.com`. If SMTP_SERVER is not defined, email verification will be disabled. |
| `TLS_CONTACT_ADDRESS` | string |  | Optional email to use as contact for [LetsEncrypt](https://letsencrypt.org/) certificates, e.g. `jdoe@example.com`. |
| `TLS_DOMAIN_NAME` | string |  | If non-empty, enables TLS (http**s**) and configures domain name of your container, e.g. `www.example.com`. In order for TLS to work you have to expose your HTTPS port to the Internet and correctly configure DNS. It WILL FAIL with `localhost` or unroutable IPs. |
| `UID_ENCRYPTION_KEY` | string | `la6YsO+bNX/+XIkOqc5Svw==` | base64-encoded 16 random bytes used as an encryption key for user IDs. |

A convenient way to generate a desired number of random bytes and base64-encode them on Linux and Mac:
```
$ openssl rand -base64 <desired length>
```
