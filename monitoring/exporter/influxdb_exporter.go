package main

import (
	"bytes"
	"log"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// InfluxDBExporter collects metrics from a Tinode server and pushes them to InfluxDB.
type InfluxDBExporter struct {
	targetAddress string
	organization  string
	bucket        string
	tokenHeader   string
	scraper       *Scraper
}

// NewInfluxDBExporter returns an initialized InfluxDB exporter.
func NewInfluxDBExporter(pushBaseAddress, organization, bucket, token string, scraper *Scraper) *InfluxDBExporter{
	targetAddress := formPushTargetAddress(pushBaseAddress, organization, bucket)
	tokenHeader   := fmt.Sprintf("Token %s", token)
	return &InfluxDBExporter{
		targetAddress: targetAddress,
		organization:  organization,
		bucket:        bucket,
		tokenHeader:   tokenHeader,
		scraper:       scraper,
	}
}

// Push scrapes metrics from Tinode server and pushes these metrics to InfluxDB.
func (e *InfluxDBExporter) Push() (string, error) {
	metrics, err := e.scraper.CollectRaw()
	if err != nil {
		return "", err
	}
	b := new(bytes.Buffer)
	ts := time.Now().UnixNano()
	for k, v := range metrics {
		fmt.Fprintf(b, "%s value=%f %d\n", k, v, ts)
	}
	req, err := http.NewRequest("POST", e.targetAddress, b)
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", e.tokenHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return resp.Status, nil
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
