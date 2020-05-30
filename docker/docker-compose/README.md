# Docker compose for end-to-end setup.

These reference docker-compose files will run MidnightChat with the MySql backend either as [a single-instance](single-instance.yml) or [a 3-node cluster](cluster.yml) setup.

```
docker-compose -f <name of the file> [-f <name of override file>] up -d
```

By default, this command starts up a mysql instance, MidnightChat server(s) and MidnightChat exporter(s).
MidnightChat server(s) is(are) configured similar to [MidnightChat Demo/Sandbox](../../README.md#demosandbox) and
maps its web port to the host's port 6060 (6061, 6062).
MidnightChat exporter(s) serve(s) metrics for Prometheus. Port mapping is 6222 (6223, 6224).

Reference configuration for [RethinkDB 2.4.0](https://hub.docker.com/_/rethinkdb?tab=tags) and [MongoDB 4.2.3](https://hub.docker.com/_/mongo?tab=tags) is also available
in the override files.

## Commands

### Full stack
To bring up the full stack, you can use the following commands:
* MySql:
  - Single-instance setup: `docker-compose -f single-instance.yml up -d`
  - Cluster: `docker-compose -f cluster.yml up -d`
* RethinkDb:
  - Single-instance setup: `docker-compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d`
  - Cluster: `docker-compose -f cluster.yml -f cluster.rethinkdb.yml up -d`
* MongoDb:
  - Single-instance setup: `docker-compose -f single-instance.yml -f single-instance.mongodb.yml up -d`
  - Cluster: `docker-compose -f cluster.yml -f cluster.mongodb.yml up -d`

You can run individual/separate components of the setup by providing their names to the `docker-compose` command.
E.g. to start the MidnightChat server in the single-instance MySql setup,
```
docker-compose -f single-instance.yml up -d MidnightChat-0
```

### Database resets and/or version upgrades
To reset the database or upgrade the database version, you can set `RESET_DB` or `UPGRADE_DB` environment variable to true when starting MidnightChat with docker-compose.
E.g. for upgrading the database in MongoDb cluster setup, use:
```
UPGRADE_DB=true docker-compose -f cluster.yml -f cluster.mongodb.yml up -d MidnightChat-0
```

For resetting the database in RethinkDb single-instance setup, run:
```
RESET_DB=true docker-compose -f single-instance.yml -f single-instance.rethinkdb.yml up -d MidnightChat-0
```

## Troubleshooting
Print out and verify your docker-compose configuration by running:
```
docker-compose -f <name of the file> [-f <name of override file>] config
```

If the MidnightChat server(s) are failing, you can print the job's stdout/stderr with:
```
docker logs MidnightChat-<instance number>
```

Additionally, you can examine the jobs `MidnightChat.log` file. To download it from the container, run:
```
docker cp MidnightChat-<instance number>:/var/log/MidnightChat.log .
```
