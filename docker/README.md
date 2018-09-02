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

### Reset data in the database

If you want to reset the data in the database, shut down the Tinode container and remove it:
```
$ docker stop tinode-srv && docker rm tinode-srv
```
then repeat step 4 adding `--env RESET_DB=true`.


### Run the chatbot

See [instructions](../chatbot/).

## Supported environment variables

You can specify the following environment variables when issuing `docker run` command:

| Variable | Type | Default | Function |
| --- | --- | --- | --- |
| `AUTH_TOKEN_KEY` | string | `wfaY2RgF2S1OQI/ZlK+LS​rp1KB2jwAdGAIHQ7JZn+Kc=` | base64-encoded 32 random bytes used as salt for authentication tokens. |
| `DEBUG_EMAIL_VERIFICATION_CODE` | string |  | Enable dummy email verification code, e.g. `123456`. Disabled by default (empty string). |
| `MYSQL_DSN` | string | `'root@tcp(mysql)/tinode'` | MySQL [DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name). |
| `RESET_DB` | bool | `false` | Drop and recreate the database. |
| `SMTP_PASSWORD` | string |  | Password to use for authentication with the SMTP server. |
| `SMTP_PORT` | number |  | Port number of the SMTP server to use for sending verification emails, e.g. `25` or `587`. |
| `SMTP_SENDER` | string |  | [RFC 5322](https://tools.ietf.org/html/rfc5322) email address to use in the `FROM` field of verification emails and for authentication with the SMTP server, .e.g. `'"John Doe" <jdoe@example.com>'`. |
| `SMTP_SERVER` | string |  | Name of the SMTP server to use for sending verification emails, e.g. `smtp.gmail.com`. If SMTP_SERVER is not defined, email verification will be disabled. |
| `TLS_CONTACT_ADDRESS` | string |  | Optional email to use as contact for [LetsEncrypt](https://letsencrypt.org/) certificates, e.g. `jdoe@example.com`. |
| `TLS_DOMAIN_NAME` | string |  | If non-empty, enables TLS (http**s**) and configures domain name of your container, e.g. `www.example.com`. In order for TLS to work you have to expose your HTTPS port to the Internet and correctly configure DNS. It WILL FAIL with `localhost` or unroutable IPs. |
| `UID_ENCRYPTION_KEY` | string | `la6YsO+bNX/+XIkOqc5Svw==` | base64-encoded 16 random bytes used as an encryption key for user IDs. |

A convenient way to generate a desired number of random bytes and base64-encode them on Linux and Mac:
```
$ openssl rand -base64 <desired length>
```