package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InfluxDBExporter collects metrics from a Tinode server and pushes them to InfluxDB.
type InfluxDBExporter struct {
	targetAddress string
	organization  string
	bucket        string
	tokenHeader   string
	instance      string
	scraper       *Scraper
}

// NewInfluxDBExporter returns an initialized InfluxDB exporter.
func NewInfluxDBExporter(influxDBVersion, pushBaseAddress, organization,
	bucket, token, instance string, scraper *Scraper) *InfluxDBExporter {

	targetAddress := formPushTargetAddress(influxDBVersion, pushBaseAddress, organization, bucket)
	tokenHeader := formAuthorizationHeaderValue(influxDBVersion, token)
	return &InfluxDBExporter{
		targetAddress: targetAddress,
		organization:  organization,
		bucket:        bucket,
		tokenHeader:   tokenHeader,
		instance:      instance,
		scraper:       scraper,
	}
}

// Push scrapes metrics from Tinode server and pushes these metrics to InfluxDB.
func (e *InfluxDBExporter) Push() error {
	metrics, err := e.scraper.CollectRaw()
	if err != nil {
		return err
	}
	b := new(bytes.Buffer)
	ts := time.Now().UnixNano()
	for k, v := range metrics {
		fmt.Fprintf(b, "%s,instance=%s value=%f %d\n", k, e.instance, v, ts)
	}
	req, err := http.NewRequest("POST", e.targetAddress, b)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", e.tokenHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var body string
		if rb, err := ioutil.ReadAll(resp.Body); err != nil {
			body = err.Error()
		} else {
			body = strings.TrimSpace(string(rb))
		}

		return fmt.Errorf("HTTP %s: %s", resp.Status, body)
	}
	return nil
}

func formPushTargetAddress(influxDBVersion, baseAddr, organization, bucket string) string {
	url, err := url.ParseRequestURI(baseAddr)
	if err != nil {
		log.Fatal("Invalid push_addr", err)
	}
	// Url format
	// - in 2.0: /api/v2/write?org=organization&bucket=bucket
	// - in 1.7: /write?db=organization
	organizationParamName := "org"
	bucketParamName := "bucket"
	if influxDBVersion == "1.7" {
		organizationParamName = "db"
		// Concept of explicit bucket in 1.7 is absent.
		bucketParamName = ""
	}
	q := url.Query()
	q.Add(organizationParamName, organization)
	if bucketParamName != "" {
		q.Add(bucketParamName, bucket)
	}
	url.RawQuery = q.Encode()
	return url.String()
}

func formAuthorizationHeaderValue(influxDBVersion, token string) string {
	// Authorization header has value
	// - in 2.0: Token <token>
	// - in 1.7: Bearer <token>
	if influxDBVersion == "2.0" {
		return fmt.Sprintf("Token %s", token)
	}
	return fmt.Sprintf("Bearer %s", token)
}
