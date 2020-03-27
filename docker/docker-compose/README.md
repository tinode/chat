# Docker compose for end-to-end setup.

These reference docker-compose files will run Tinode with the MySql backend either as [a single-instance](single-instance.yml) or [a 3-node cluster](cluster.yml) setup.

```
docker-compose -f <name of the file> up -d
```

By default, this command starts up a mysql instance, Tinode server(s) and Tinode exporter(s).
Tinode server(s) is(are) configured similar to [Tinode Demo/Sandbox](../../README.md#demosandbox) and
maps its web port to the host's port 6060 (6061, 6062).
Tinode exporter(s) serve(s) metrics for Prometheus. Port mapping is 6222 (6223, 6224).

Reference configuration for [RethinkDB 2.4.0](https://hub.docker.com/_/rethinkdb?tab=tags) and [MongoDB 4.2.3](https://hub.docker.com/_/mongo?tab=tags) is also available
in the form of override files.

Start single-instance setup with:
```
docker-compose -f single-instance.yml -f single-instance.<DATABASE>.yml up -d
```

And cluster with:
```
docker-compose -f cluster.yml -f cluster.<DATABASE>.yml up -d
```
