# Docker compose for end-to-end setup.

These reference docker-compose files will run Tinode either as [a single-instance](single-instance.yml) or [a 3-node cluster](cluster.yml) setup.
They both use MySQL backend.

```
docker-compose -f <name of the file> up -d
```

By default, this command starts up a mysql instance, Tinode server(s) and Tinode exporter(s).
Tinode server(s) is(are) configured similar to [Tinode Demo/Sandbox](../../README.md#demosandbox) and
maps its web port to the host's port 6060 (6061, 6062).
Tinode exporter(s) serve(s) metrics for Prometheus. Port mapping is 6222 (6223, 6224).
