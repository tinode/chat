package main

import (
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PromExporter collects metrics in Prometheus format from a Tinode server.
type PromExporter struct {
	address   string
	timeout   time.Duration
	namespace string

	scraper *Scraper

	up               *prometheus.Desc
	version          *prometheus.Desc
	topicsLive       *prometheus.Desc
	topicsTotal      *prometheus.Desc
	sessionsLive     *prometheus.Desc
	sessionsTotal    *prometheus.Desc
	clusterLeader    *prometheus.Desc
	clusterSize      *prometheus.Desc
	clusterNodesLive *prometheus.Desc
	malloced         *prometheus.Desc
}

// NewPromExporter returns an initialized Prometheus exporter.
func NewPromExporter(server, namespace string, timeout time.Duration, scraper *Scraper) *PromExporter {
	return &PromExporter{
		address:   server,
		timeout:   timeout,
		namespace: namespace,
		scraper:   scraper,
		up: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "up"),
			"If tinode instance is reachable.",
			nil,
			nil,
		),
		version: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "version"),
			"The version of this tinode instance.",
			nil,
			nil,
		),
		topicsLive: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "topics_live_count"),
			"Number of currently active topics.",
			nil,
			nil,
		),
		topicsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "topics_total"),
			"Total number of topics used during instance lifetime.",
			nil,
			nil,
		),
		sessionsLive: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sessions_live_count"),
			"Number of currently active sessions.",
			nil,
			nil,
		),
		sessionsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sessions_total"),
			"Total number of sessions since instance start.",
			nil,
			nil,
		),
		clusterLeader: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cluster_leader"),
			"If this cluster node is the cluster leader.",
			nil,
			nil,
		),
		clusterSize: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cluster_size"),
			"Configured number of cluster nodes.",
			nil,
			nil,
		),
		clusterNodesLive: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cluster_nodes_live"),
			"Number of cluster nodes believed to be live by the current node.",
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
func (e *PromExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up
	ch <- e.version
	ch <- e.topicsLive
	ch <- e.topicsTotal
	ch <- e.sessionsLive
	ch <- e.sessionsTotal
	ch <- e.clusterLeader
	ch <- e.clusterSize
	ch <- e.clusterNodesLive
	ch <- e.malloced
}

// Collect fetches statistics from the configured Tinode instance, and
// delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *PromExporter) Collect(ch chan<- prometheus.Metric) {
	up := float64(1)
	if stats, err := e.scraper.Scrape(); err != nil {
		log.Println("Failed to fetch or parse response", err)
		up = 0
	} else {
		if err := e.parseStats(ch, stats); err != nil {
			up = 0
		}
	}

	ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, up)
}

func (e *PromExporter) parseStats(ch chan<- prometheus.Metric, stats map[string]interface{}) error {
	err := firstError(
		e.parseAndUpdate(ch, e.version, prometheus.GaugeValue, stats, "Version"),
		e.parseAndUpdate(ch, e.topicsLive, prometheus.GaugeValue, stats, "LiveTopics"),
		e.parseAndUpdate(ch, e.topicsTotal, prometheus.CounterValue, stats, "TotalTopics"),
		e.parseAndUpdate(ch, e.sessionsLive, prometheus.GaugeValue, stats, "LiveSessions"),
		e.parseAndUpdate(ch, e.sessionsTotal, prometheus.CounterValue, stats, "TotalSessions"),
		e.parseAndUpdate(ch, e.clusterLeader, prometheus.GaugeValue, stats, "ClusterLeader"),
		e.parseAndUpdate(ch, e.clusterSize, prometheus.GaugeValue, stats, "TotalClusterNodes"),
		e.parseAndUpdate(ch, e.clusterNodesLive, prometheus.GaugeValue, stats, "LiveClusterNodes"),
		e.parseAndUpdate(ch, e.malloced, prometheus.GaugeValue, stats, "memstats.Alloc"),
	)

	return err
}

func (e *PromExporter) parseAndUpdate(ch chan<- prometheus.Metric, desc *prometheus.Desc, valueType prometheus.ValueType,
	stats map[string]interface{}, key string) error {
	if v, err := parseMetric(stats, key); err == nil {
		ch <- prometheus.MustNewConstMetric(desc, valueType, v)
		return nil
	} else {
		return err
	}
}

func firstError(errs ...error) error {
	for _, v := range errs {
		if v != nil {
			return v
		}
	}
	return nil
}
