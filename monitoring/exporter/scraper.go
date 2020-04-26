package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

// Scraper collects metrics from a tinode server.
type Scraper struct {
	address string
	metrics []string
}

var errKeyNotFound = errors.New("key not found")

// CollectRaw gathers all metrics from the configured Tinode instance,
// and returns them as a map.
func (s *Scraper) CollectRaw() (map[string]float64, error) {
	if stats, err := s.Scrape(); err != nil {
		log.Println("Failed to fetch or parse response", err)
		return nil, err
	} else {
		if metrics, err := s.parseStatsRaw(stats); err != nil {
			return nil, err
		} else {
			metrics["up"] = 1
			return metrics, nil
		}
	}
}

// Scrape fetches the data from Tinode server using HTTP GET then decodes the response.
func (s *Scraper) Scrape() (map[string]interface{}, error) {
	resp, err := http.Get(s.address)
	if err != nil {
		log.Println("Failed to connect to server", err)
		return nil, err
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&stats)
	return stats, err
}

func (s *Scraper) parseStatsRaw(stats map[string]interface{}) (map[string]float64, error) {
	metrics := make(map[string]float64)
	for _, key := range s.metrics {
		if val, err := parseMetric(stats, key); err == nil {
			metrics[key] = val
		} else {
			return nil, err
		}
	}
	return metrics, nil
}

func parseMetric(stats map[string]interface{}, key string) (float64, error) {
	v, err := parseNumeric(stats, key)

	if err == errKeyNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v, nil
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
