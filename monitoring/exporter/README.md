# Tinode Metric Exporter

This is a simple service which reads JSON monitoring data exposed by Tinode server using [expvar](https://golang.org/pkg/expvar/) and re-publishes it in other formats. Currently the supported formats are:

* [InfluxDB](https://www.influxdata.com/) [exporter](https://docs.influxdata.com/influxdb/v1.7/tools/api/#write-http-endpoint) **pushes** data to its target backend. This is the default mode.
* [Prometheus](https://prometheus.io/) [exporter](https://prometheus.io/docs/instrumenting/exporters/) which exports data in [prometheus format](https://prometheus.io/docs/concepts/data_model/). The Prometheus monitoring service is expected to **pull/scrape** data from the  exporter.

## Usage

Exporters are intended to run next to (pair with) Tinode servers: one Exporter per one Tinode server, i.e. a single Exporter provides metrics from a single Tinode server. 

## Configuration

The exporters are configured by command-line flags:

### Common flags
* `serve_for` specifies which monitoring service the Exporter will gather metrics for; accepted values: `influxdb`, `prometheus`; default: `influxdb`.
* `tinode_addr` is the address where the Tinode instance publishes `expvar` data to scrape; default: `http://localhost:6060/stats/expvar`.
* `listen_at` is the hostname to bind to for serving the metrics; default: `:6222`.
* `instance` is the Exporter instance name (it may be exported to the upstream backend); default: `exporter`.
* `metric_list` is a comma-separated list of metrics to export; default: `Version,LiveTopics,TotalTopics,LiveSessions,ClusterLeader,TotalClusterNodes,LiveClusterNodes,memstats.Alloc`.

### InfluxDB
* `influx_push_addr` is the address of InfluxDB target server where the data gets sent; default: `http://localhost:9999/write`.
* `influx_db_version` is the version of InfluxDB (only 1.7 and 2.0 are supported); default: `1.7`.
* `influx_organization` specifies InfluxDB organization to push metrics as; default: `test`;
* `influx_bucket` is the name of InfluxDB storage bucket to store data in (used only in InfluxDB 2.0); default: `test`.
* `influx_auth_token` - InfluxDB authentication token; no default value.
* `influx_push_interval` - InfluxDB push interval in seconds; default: `30`.

#### Example

Run InfluxDB Exporter as
```
./exporter \
    --serve_for=influxdb \
    --tinode_addr=http://localhost:6060/stats/expvar \
    --listen_at=:6222 \
    --instance=exp-0 \
    --influx_push_addr=http://my-influxdb-backend.net/write \
    --influx_db_version=1.7 \
    --influx_organization=myOrg \
    --influx_auth_token=myAuthToken123 \
    --influx_push_interval=30
```

This exporter will push the collected metrics to the specified backend once every 30 seconds.


### Prometheus
* `prom_namespace` is a prefix to use for metrics names. If you are monitoring multiple tinode instances you may want to use different namespaces; default: `tinode`.
* `prom_metrics_path` is the path under which to expose the metrics for scraping; default: `/metrics`.
* `prom_timeout` is the Tinode connection timeout in seconds in response to Prometheus scrapes; default: `15`.

#### Example
Run Prometheus Exporter as
```
./exporter \
    --serve_for=prometheus \
    --tinode_addr=http://localhost:6060/stats/expvar \
    --listen_at=:6222 \
    --instance=exp-0 \
    --prom_namespace=tinode \
    --prom_metrics_path=/metrics \
    --prom_timeout=15
```

This exporter will serve data at path /metrics, on port 6222.
Once running, configure your Prometheus monitoring installation to collect data from this exporter.
