# Docker compose for end-to-end setup.

These reference docker-compose files will run Tinode with the MySql backend either as [a single-instance](single-instance.yml) or [a 3-node cluster](cluster.yml) setup.

```
docker compose -f <name of the file> [-f <name of override file>] up -d
```

By default, this command starts up a mysql instance, Tinode server(s) and Tinode exporter(s).
Tinode server(s) is(are) configured similar to [Tinode Demo/Sandbox](../../README.md#demosandbox) and
maps its web port to the host's port 6060 (6061, 6062). Tinode exporter(s) serve(s) metrics for InfluxDB.

Reference configuration for the following databases is also available in the override files:
* [PostgreSQL 15 (latest minor, Debian Bookworm)](https://hub.docker.com/_/postgres/tags)
* [MongoDB 8.0.38](https://hub.docker.com/_/mongo/tags)
* [RethinkDB 2.4.4](https://hub.docker.com/_/rethinkdb/tags)


## Commands

### Full stack
To bring up the full stack, you can use the following commands:
* MySql:
  - Single-instance setup: `docker compose -f single-instance.yml up -d`
  - Cluster: `docker compose -f cluster.yml up -d`
* PostgreSQL:
  - Single-instance setup: `docker compose -f single-instance.yml -f single-instance.postgres.yml up -d`
  - Cluster: `docker compose -f cluster.yml -f cluster.postgres.yml up -d`
* MongoDb:
  - Single-instance setup: `docker compose -f single-instance.yml -f single-instance.mongodb.yml up -d`
  - Cluster: `docker compose -f cluster.yml -f cluster.mongodb.yml up -d`
* RethinkDb:
  - Single-instance setup: `docker compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d`
  - Cluster: `docker compose -f cluster.yml -f cluster.rethinkdb.yml up -d`

You can run individual/separate components of the setup by providing their names to the `docker compose` command.
E.g. to start the Tinode server in the single-instance MySql setup,
```
docker compose -f single-instance.yml up -d tinode-0
```

### Database resets and/or version upgrades
To reset the database or upgrade the database version, you can set `RESET_DB` or `UPGRADE_DB` environment variable to true when starting Tinode with Docker Compose.
E.g. for upgrading the database in MongoDb cluster setup, use:
```
UPGRADE_DB=true docker compose -f cluster.yml -f cluster.mongodb.yml up -d tinode-0
```

For resetting the database in RethinkDb single-instance setup, run:
```
RESET_DB=true docker compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d tinode-0
```

## Troubleshooting
Print out and verify your docker-compose configuration by running:
```
docker compose -f <name of the file> [-f <name of override file>] config
```

If the Tinode server(s) are failing, you can print the job's stdout/stderr with:
```
docker logs tinode-<instance number>
```

Additionally, you can examine the jobs `tinode.log` file. To download it from the container, run:
```
docker cp tinode-<instance number>:/var/log/tinode.log .
```

## Upgrading existing databases

The defaults are MySQL 8.4.13, MongoDB 8.0.38 and RethinkDB 2.4.4.
Back up existing data and rehearse on a restored copy before switching images.
MySQL 5.7 must first reach its latest patch, then a supported 8.0 patch, before
8.4. Existing 8.0 installations must run the MySQL Shell upgrade checker before
moving to 8.4. See https://dev.mysql.com/doc/refman/8.4/en/upgrade-paths.html.
MySQL native-password accounts must be migrated to caching_sha2_password, or
explicitly enable mysql_native_password temporarily on 8.4 while migrating.

MongoDB 4.2 cannot use the 8.0 image directly with existing data. Upgrade through
4.4, 5.0, 6.0 and 7.0, using the required patches and feature compatibility version
at every step, then 8.0. Check driver compatibility and the vendor procedure:
https://www.mongodb.com/docs/manual/release-notes/8.0-upgrade-replica-set/.
The MongoDB examples now use mongosh and wait for a writable replica-set primary.

These examples allow an empty MySQL root password for local evaluation. Configure
credentials and the Tinode DSN before using them outside a trusted local setup.
The INFLUXDB_VERSION value 1.7 selects the exporter's 1.x protocol and is not an
image version; retain it with newer InfluxDB 1.x servers.
