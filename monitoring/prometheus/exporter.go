package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Exporter collects metrics from a tinode server.
type Exporter struct {
	address   string
	timeout   time.Duration
	namespace string

	up            *prometheus.Desc
	uptime        *prometheus.Desc
	version       *prometheus.Desc
	topicsLive    *prometheus.Desc
	topicsTotal   *prometheus.Desc
	sessionsLive  *prometheus.Desc
	sessionsTotal *prometheus.Desc
	malloced      *prometheus.Desc
}

var errKeyNotFound = errors.New("key not found")

// NewExporter returns an initialized exporter.
func NewExporter(server, namespace string, timeout time.Duration) *Exporter {
	return &Exporter{
		address:   server,
		timeout:   timeout,
		namespace: namespace,
		up: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "up"),
			"If tinode server is reachable.",
			nil,
			nil,
		),
		uptime: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "uptime_seconds"),
			"Number of seconds since the server started.",
			nil,
			nil,
		),
		version: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "version"),
			"The version of this tinode server.",
			[]string{"version"},
			nil,
		),
		topicsLive: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "topics_live_count"),
			"Number of currenly active topics.",
			nil,
			nil,
		),
		topicsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "topics_total"),
			"Total number of topics used during server lifetime.",
			nil,
			nil,
		),
		sessionsLive: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sessions_live_count"),
			"Number of currenly active sessions.",
			nil,
			nil,
		),
		sessionsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sessions_total"),
			"Total number of sessions since server start.",
			nil,
			nil,
		),
		malloced: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "malloced_bytes"),
			"Number of bytes of memory allocated and in use.",
			nil,
			nil,
		),
	}
}

// Describe describes all the metrics exported by the memcached exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up
	ch <- e.uptime
	ch <- e.version
	ch <- e.topicsLive
	ch <- e.topicsTotal
	ch <- e.sessionsLive
	ch <- e.sessionsTotal
	ch <- e.malloced
}

// Collect fetches statistics from the configured Tinode instance, and
// delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	resp, err := http.Get(e.address)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 0)
		log.Println("Failed to connect to server", err)
		return
	}
	defer resp.Body.Close()

	up := float64(1)

	var stats map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&stats)
	if err != nil {
		log.Println("Failed to fetch or parse response", err)
		up = 0
	} else {
		if err := e.parseStats(ch, stats); err != nil {
			up = 0
		}
	}

	ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, up)
}

func (e *Exporter) parseStats(ch chan<- prometheus.Metric, stats map[string]interface{}) error {

	err := firstError(
		e.parseAndUpdate(ch, e.version, prometheus.GaugeValue, stats, "Version"),
		e.parseAndUpdate(ch, e.uptime, prometheus.CounterValue, stats, "uptime"),
		e.parseAndUpdate(ch, e.topicsLive, prometheus.GaugeValue, stats, "LiveTopics"),
		e.parseAndUpdate(ch, e.topicsTotal, prometheus.CounterValue, stats, "TotalTopics"),
		e.parseAndUpdate(ch, e.sessionsLive, prometheus.GaugeValue, stats, "LiveSessions"),
		e.parseAndUpdate(ch, e.sessionsTotal, prometheus.CounterValue, stats, "TotalSessions"),
		e.parseAndUpdate(ch, e.malloced, prometheus.GaugeValue, stats, "memstats.Alloc"),
	)

	return err
}

func (e *Exporter) parseAndUpdate(ch chan<- prometheus.Metric, desc *prometheus.Desc, valueType prometheus.ValueType,
	stats map[string]interface{}, key string) error {

	v, err := parseNumeric(stats, key)

	if err == errKeyNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	ch <- prometheus.MustNewConstMetric(desc, valueType, v)
	return nil
}

func firstError(errs ...error) error {
	for _, v := range errs {
		if v != nil {
			return v
		}
	}
	return nil
}

func parseNumeric(stats map[string]interface{}, path string) (float64, error) {
	parts := strings.Split(path, ".")
	var value interface{}
	var found bool
	value = stats
	for i := 0; i < len(parts); i++ {
		subset, ok := value.(map[string]interface{})
		if !ok {
			log.Println("Invalid key path:", path)
			return 0, errKeyNotFound
		}
		value, found = subset[parts[i]]
		if !found {
			log.Println("Invalid key path:", path, "(", parts[i], ")")
			return 0, errKeyNotFound
		}
	}

	floatval, ok := value.(float64)
	if !ok {
		log.Println("Value at path is not a float64:", path, value)
		return 0, errKeyNotFound
	}

	return floatval, nil
}
