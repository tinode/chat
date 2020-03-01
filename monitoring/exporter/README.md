# Prometheus `expvar` Exporter

This is a [prometheus](https://prometheus.io/) [exporter](https://prometheus.io/docs/instrumenting/exporters/): a service which reads JSON monitoring data exposed by Tinode server using [expvar](https://golang.org/pkg/expvar/) and re-publishes it in [prometheus format](https://prometheus.io/docs/concepts/data_model/).

## Usage

Run this service as
```
./prometheus --tinode_addr=http://localhost:6060/stats/expvar \
    --namespace=tinode --listen_at=:6222 --metrics_path=/metrics
```

* `tinode_addr` is the address where the Tinode instance publishes `expvar` data to scrape.
* `namespace` is a prefix to use for metrics names. If you are monitoring multiple tinode instances you may want to use different namespaces.
* `listen_at` is the hostname to bind to for serving the metrics.
* `metrics_path` path under which to expose the metrics.

Once running, configure your Prometheus monitoring installation to collect data from this exporter.
