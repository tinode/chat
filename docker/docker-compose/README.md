# Docker compose for end-to-end setup.

These reference docker-compose files will run Tinode with the MySql backend either as [a single-instance](single-instance.yml) or [a 3-node cluster](cluster.yml) setup.

```
docker-compose -f <name of the file> [-f <name of override file>] up -d
```

By default, this command starts up a mysql instance, Tinode server(s) and Tinode exporter(s).
Tinode server(s) is(are) configured similar to [Tinode Demo/Sandbox](../../README.md#demosandbox) and
maps its web port to the host's port 6060 (6061, 6062). Tinode exporter(s) serve(s) metrics for InfluxDB.

Reference configuration for the following databases is also available in the override files:
* [PostgreSQL 15.2](https://hub.docker.com/_/postgres/tags)
* [MongoDB 4.2.3](https://hub.docker.com/_/mongo/tags)
* [RethinkDB 2.4.2](https://hub.docker.com/_/rethinkdb/tags)


## Commands

### Full stack
To bring up the full stack, you can use the following commands:
* MySql:
  - Single-instance setup: `docker-compose -f single-instance.yml up -d`
  - Cluster: `docker-compose -f cluster.yml up -d`
* PostgreSQL:
  - Single-instance setup: `docker-compose -f single-instance.yml -f single-instance.postgres.yml up -d`
  - Cluster: `docker-compose -f cluster.yml -f cluster.postgres.yml up -d`
* MongoDb:
  - Single-instance setup: `docker-compose -f single-instance.yml -f single-instance.mongodb.yml up -d`
  - Cluster: `docker-compose -f cluster.yml -f cluster.mongodb.yml up -d`
* RethinkDb:
  - Single-instance setup: `docker-compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d`
  - Cluster: `docker-compose -f cluster.yml -f cluster.rethinkdb.yml up -d`

You can run individual/separate components of the setup by providing their names to the `docker-compose` command.
E.g. to start the Tinode server in the single-instance MySql setup,
```
docker-compose -f single-instance.yml up -d tinode-0
```

### Database resets and/or version upgrades
To reset the database or upgrade the database version, you can set `RESET_DB` or `UPGRADE_DB` environment variable to true when starting Tinode with docker-compose.
E.g. for upgrading the database in MongoDb cluster setup, use:
```
UPGRADE_DB=true docker-compose -f cluster.yml -f cluster.mongodb.yml up -d tinode-0
```

For resetting the database in RethinkDb single-instance setup, run:
```
RESET_DB=true docker-compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d tinode-0
```

## Troubleshooting
Print out and verify your docker-compose configuration by running:
```
docker-compose -f <name of the file> [-f <name of override file>] config
```

If the Tinode server(s) are failing, you can print the job's stdout/stderr with:
```
docker logs tinode-<instance number>
```

Additionally, you can examine the jobs `tinode.log` file. To download it from the container, run:
```
docker cp tinode-<instance number>:/var/log/tinode.log .
```
