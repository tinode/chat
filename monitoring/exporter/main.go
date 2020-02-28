package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

type promHTTPLogger struct{}

func (l promHTTPLogger) Println(v ...interface{}) {
	log.Println(v...)
}

func formPushTargetAddress(baseAddr, organization, bucket string) string {
	url, err := url.ParseRequestURI(baseAddr)
	if err != nil {
		log.Fatal("Invalid push_addr", err)
	}
	q := url.Query()
	q.Add("org", organization)
	q.Add("bucket", bucket)
	url.RawQuery = q.Encode()
	return url.String()
}

func main() {
	log.Printf("Tinode metrics exporter.")

	var (
		tinodeAddr   = flag.String("tinode_addr", "http://localhost:6060/stats/expvar", "Address of the Tinode instance to scrape.")
		namespace    = flag.String("namespace", "tinode", "Namespace for metrics '<namespace>_...'")
		listenAt     = flag.String("listen_at", ":6222", "Host name and port to serve collected metrics at.")
		metricsPath  = flag.String("metrics_path", "/metrics", "Path under which to expose metrics.")
		timeout      = flag.Int("timeout", 15, "Tinode connection timeout in seconds.")

		// InfluxDB-specific arguments.
		pushAddr     = flag.String("push_addr", "http://localhost:9999/api/v2/write", "Address of push target server where the data gets sent.")
		organization = flag.String("organization", "test", "Organization to push metrics as.")
		bucket       = flag.String("bucket", "test", "Storage bucket to store data in.")
		authToken    = flag.String("auth_token", "", "Push authentication token.")
	)
	flag.Parse()

	if *metricsPath == "/" {
		log.Fatal("Serving metrics from / is not supported")
	}
	if *organization == "" {
		log.Fatal("Must specify --organization")
	}
	if *authToken == "" {
		log.Fatal("Must specify --auth_token")
	}
	if *bucket == "" {
		log.Fatal("Must specify --bucket")
	}
	targetAddress := formPushTargetAddress(*pushAddr, *organization, *bucket)
	tokenHeader := fmt.Sprintf("Token %s", *authToken)

	exporter := NewExporter(*tinodeAddr, *namespace, time.Duration(*timeout)*time.Second)
	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)
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

	// Forces a data push.
	http.HandleFunc("/push", func(w http.ResponseWriter, r *http.Request) {
		var msg string
		if metrics, err := exporter.CollectRaw(); err == nil {
			b := new(bytes.Buffer)
			ts := time.Now().UnixNano()
			for k, v := range metrics {
				fmt.Fprintf(b, "%s value=%f %d\n", k, v, ts)
			}
			if req, err := http.NewRequest("POST", targetAddress, b); err == nil {
				req.Header.Add("Authorization", tokenHeader)
				if resp, err := http.DefaultClient.Do(req); err != nil {
					msg = "Fail: " + err.Error()
				} else {
					msg = "ok - " + resp.Status
					resp.Body.Close()
				}
			} else {
				msg = "Fail: " + err.Error()
			}
		} else {
			msg = "Fail: " + err.Error()
		}

		w.Write([]byte(`<html><head><title>Tinode Push</title></head><body>
<h1>Tinode Push</h1>
<pre>` + msg + `</pre>
</body></html>`))
	})

	log.Println("Reading Tinode expvar from", *tinodeAddr)
	log.Printf("Serving metrics at %s%s", *listenAt, *metricsPath)
	log.Fatalln(http.ListenAndServe(*listenAt, nil))
}
