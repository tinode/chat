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

	up                            *prometheus.Desc
	version                       *prometheus.Desc
	topicsLive                    *prometheus.Desc
	topicsTotal                   *prometheus.Desc
	sessionsLive                  *prometheus.Desc
	sessionsTotal                 *prometheus.Desc

	incomingMessagesWebsockTotal  *prometheus.Desc
	outgoingMessagesWebsockTotal  *prometheus.Desc

	incomingMessagesLongpollTotal *prometheus.Desc
	outgoingMessagesLongpollTotal *prometheus.Desc

	incomingMessagesGrpcTotal     *prometheus.Desc
	outgoingMessagesGrpcTotal     *prometheus.Desc

	fileDownloadsTotal            *prometheus.Desc
	fileUploadsTotal              *prometheus.Desc

	clusterLeader                 *prometheus.Desc
	clusterSize                   *prometheus.Desc
	clusterNodesLive              *prometheus.Desc
	malloced                      *prometheus.Desc
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
		incomingMessagesWebsockTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "incoming_messages_websock_total"),
			"Total number of incoming messages via websocket.",
			nil,
			nil,
		),
		outgoingMessagesWebsockTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "outgoing_messages_websock_total"),
			"Total number of outgoiing messages via websocket.",
			nil,
			nil,
		),
		incomingMessagesLongpollTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "incoming_messages_longpoll_total"),
			"Total number of incoming messages via longpoll.",
			nil,
			nil,
		),
		outgoingMessagesLongpollTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "outgoing_messages_longpoll_total"),
			"Total number of outgoiing messages via longpoll.",
			nil,
			nil,
		),
		incomingMessagesGrpcTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "incoming_messages_grpc_total"),
			"Total number of incoming messages via grpc.",
			nil,
			nil,
		),
		outgoingMessagesGrpcTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "outgoing_messages_grpc_total"),
			"Total number of outgoiing messages via grpc.",
			nil,
			nil,
		),
		fileDownloadsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "file_downloads_total"),
			"Total number of large file downloads.",
			nil,
			nil,
		),
		fileUploadsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "file_uploads_total"),
			"Total number of large file uploads.",
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

	ch <- e.incomingMessagesWebsockTotal
	ch <- e.outgoingMessagesWebsockTotal

	ch <- e.incomingMessagesLongpollTotal
	ch <- e.outgoingMessagesLongpollTotal

	ch <- e.incomingMessagesGrpcTotal
	ch <- e.outgoingMessagesGrpcTotal

	ch <- e.fileDownloadsTotal
	ch <- e.fileUploadsTotal

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

		e.parseAndUpdate(ch, e.incomingMessagesWebsockTotal, prometheus.CounterValue, stats, "IncomingMessagesWebsockTotal"),
		e.parseAndUpdate(ch, e.outgoingMessagesWebsockTotal, prometheus.CounterValue, stats, "OutgoingMessagesWebsockTotal"),

		e.parseAndUpdate(ch, e.incomingMessagesLongpollTotal, prometheus.CounterValue, stats, "IncomingMessagesLongpollTotal"),
		e.parseAndUpdate(ch, e.outgoingMessagesLongpollTotal, prometheus.CounterValue, stats, "OutgoingMessagesLongpollTotal"),

		e.parseAndUpdate(ch, e.incomingMessagesGrpcTotal, prometheus.CounterValue, stats, "IncomingMessagesGrpcTotal"),
		e.parseAndUpdate(ch, e.outgoingMessagesGrpcTotal, prometheus.CounterValue, stats, "OutgoingMessagesGrpcTotal"),

		e.parseAndUpdate(ch, e.fileDownloadsTotal, prometheus.CounterValue, stats, "FileDownloadsTotal"),
		e.parseAndUpdate(ch, e.fileUploadsTotal, prometheus.CounterValue, stats, "FileUploadsTotal"),

		e.parseAndUpdate(ch, e.clusterLeader, prometheus.GaugeValue, stats, "ClusterLeader"),
		e.parseAndUpdate(ch, e.clusterSize, prometheus.GaugeValue, stats, "TotalClusterNodes"),
		e.parseAndUpdate(ch, e.clusterNodesLive, prometheus.GaugeValue, stats, "LiveClusterNodes"),
		e.parseAndUpdate(ch, e.malloced, prometheus.GaugeValue, stats, "memstats.Alloc"),
	)

	return err
}

func (e *PromExporter) parseAndUpdate(ch chan<- prometheus.Metric, desc *prometheus.Desc, valueType prometheus.ValueType,
	stats map[string]interface{}, key string) error {
	v, err := parseMetric(stats, key)
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
