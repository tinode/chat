package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

type promHTTPLogger struct{}

func (l promHTTPLogger) Println(v ...interface{}) {
	log.Println(v...)
}

func main() {
	log.Printf("Tinode metrics exporter for Prometheus")

	var (
		tinodeAddr  = flag.String("tinode_addr", "http://localhost:6060/stats/expvar", "Address of the Tinode instance to scrape")
		namespace   = flag.String("namespace", "tinode", "Namespace for metrics '<namespace>_...'")
		listenAt    = flag.String("listen_at", ":6222", "Host name and port to serve collected metrics at.")
		metricsPath = flag.String("metrics_path", "/metrics", "Path under which to expose metrics.")
		timeout     = flag.Int("timeout", 15, "Tinode connection timeout in seconds")
	)
	flag.Parse()

	if *metricsPath == "/" {
		log.Fatal("Serving metrics from / is not supported")
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewExporter(*tinodeAddr, *namespace, time.Duration(*timeout)*time.Second))
	http.Handle(*metricsPath,
		promhttp.InstrumentMetricHandler(
			registry,
			promhttp.HandlerFor(
				registry,
				promhttp.HandlerOpts{
					ErrorLog: &promHTTPLogger{},
					Timeout:  time.Duration(*timeout) * time.Second,
				},
			),
		),
	)

	// Index page at web root.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Tinode Exporter</title></head><body>
<h1>Tinode Exporter</h1>
<p><a href="` + *metricsPath + `">Metrics</a></p>
<h2>Build</h2>
<pre>` + version.Info() + ` ` + version.BuildContext() + `</pre>
</body></html>`))
	})

	log.Println("Reading Tinode expvar from", *tinodeAddr)
	log.Printf("Serving metrics at %s%s", *listenAt, *metricsPath)
	log.Fatalln(http.ListenAndServe(*listenAt, nil))
}
