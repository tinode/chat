# Prometheus expvar exporter

This is a [prometheus](https://prometheus.io/) [exporter](https://prometheus.io/docs/instrumenting/exporters/): a service which reads JSON monitoring data exposed by Tinode server using [expvar](https://golang.org/pkg/expvar/) and re-publishes it in [prometheus format](https://prometheus.io/docs/concepts/data_model/).
