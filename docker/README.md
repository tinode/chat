# Using Docker to run Tinode

1. [Install Docker](https://docs.docker.com/install/) 1.8 or above. The provided dockerfiles are dependent on [Docker networking](https://docs.docker.com/network/) which may not work with the older Docker.

2. `cd` to the docker directory. The following instructions assume that your current directory is `tinode_root_directory/docker`, where `tinode_root_directory` is the directory where you cloned the https://github.com/tinode/chat depository.

3. Create a bridge network. It's used to connect application containers with the database container. 
	```
	$ docker network create tinode-net
	```
	
4. Decide which database backend you want to use: RethinkDB (default) or MySQL (experimental). Run the selected database container, attaching it to `tinode-net` network:

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

	
5. Build the Tinode server image. This image also includes the [web app](https://github.com/tinode/example-react-js/):
	1. **RethinkDB**
	```
	$ docker build --tag=tinode-srv tinode-server
	```
	2. **MySQL**
	```
	$ docker build --tag=tinode-srv --build-arg TARGET_DB=mysql tinode-server
	```
	
	Optionally you may want to provide a UID encryption key as `--env SNOWFLAKE_UID_KEY=base64+encoded+16+bytes=`. On most Unix-like systems you can generate such a unique key by executing something like `base64 <(head -c 16 /dev/random)`.
	 
	The system uses [snowflake](https://github.com/tinode/snowflake) to generate unique IDs for values like user IDs and topic names. To make them unpredictable it encrypts them with [XTEA](https://en.wikipedia.org/wiki/XTEA). If you don't provide the key, a default one (`la6YsO+bNX/+XIkOqc5Svw==`) will be used. As a result the IDs will be easily guessable (but still not sequential). 


6. Run Tinode server:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --network tinode-net tinode-srv
	```
	The port mapping `-p 6060:18080` tells Docker to map container's port 18080 to host's port 6060 making server accessible at http://localhost:6060/. If you provided a `SNOWFLAKE_UID_KEY` at step 5, you must provide the same key at this step too.

7. Test the installation by pointing your browser to [http://localhost:6060/x/](http://localhost:6060/x/).

## Optional

### Resetting the data

If you want to reset the data in the database, shut down the server container
```
$ docker stop tinode-srv
```
then repeat step 6 then restart the server
```
$ docker start tinode-srv --reset
```

### Running the chatbot

See [instructions](../chatbot/).

