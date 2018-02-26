# Using Docker files to run Tinode

1. [Install Docker](https://docs.docker.com/install/) 1.8 or above. The provided dockerfiles are dependent on [Docker networking](https://docs.docker.com/network/) which may may not work with the older Docker. 

2. Select which database backend you want to use: RethinDB (default) or MySQL (experimental). Run the selected database container:

	1. **RethinkDB**: If you've decided to use RethinDB backend, run the official RethinkDB Docker container:
	```
	$ docker run --name rethinkdb -d rethinkdb:2.3
	```
	See [instructions](https://hub.docker.com/_/rethinkdb/) for more options.
	
	2. **MySQL**: If you've decided to use MySQL backend, run the official MySQL Docker container:
	```
	$ docker run --name mysql -d mysql:5.7
	```
	See [instructions](https://hub.docker.com/_/mysql/) for more options.
	
	The name `rethinkdb` or `mysql` in the name assignment `--name` is important. It's used later to get database's host address and port numbers.


3. Build the initializer image from the Dockerfile provided:
	1. **RethinkDB**
	```
	$ docker build --tag=tinode-init-db init-db
	```
	2. **MySQL**
	```
	$ docker build --tag=tinode-init-db --build-arg TARGET_DB=mysql init-db
	```
	
4. Run the container to initialize the `tinode` database:
	```
	$ docker run --rm --name tinode-init tinode-init-db
	```
	Optionally you may want to provide a UID encryption key as `--env SNOWFLAKE_UID_KEY=base64+encoded+16+bytes=`. The system uses [snowflake](https://github.com/tinode/snowflake) to generate unique IDs for values like user IDs. To make them unpredictable it encrypts them with [XTEA](https://en.wikipedia.org/wiki/XTEA). If you don't provide the key, a default one will be used. As a result the IDs will be easily predictable (but still not sequential).

	At this point the database is initialized and loaded with test data. No need to do this again unless you need to reset the data or delete/recreated the DB container.

5. Build the Tinode server image from the Dockerfile:
	1. **RethinkDB**
	```
	$ docker build --tag=tinode-srv tinode-server
	```
	2. **MySQL**
	```
	$ docker build --tag=tinode-srv --build-arg TARGET_DB=mysql tinode-server
	```

6. Run Tinode server:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv tinode-srv
	```
	The port mapping `-p 6060:18080` tells Docker to map container's port 18080 to host's port 6060 making server accessible at http://localhost:6060/. If you provided a `SNOWFLAKE_UID_KEY` at step 4, you must provide the same key at this step too. Otherwise primary key collisions may happen.

7. Test the installation by pointing your browser to [http://localhost:6060/x/](http://localhost:6060/x/).

