// External services contacted through RPC
package main

import (
	"encoding/json"
	"log"
	"time"
)

const (
	plgHi = 1 << iota
	plgAcc
	plgLogin
	plgSub
	plgLeave
	plgPub
	plgGet
	plgSet
	plgDel
	plgNote

	plgCtrl
	plgData
	plgMeta
	plgPres
	plgInfo

	plgHandlerMask = plgHi | plgAcc | plgLogin | plgSub | plgLeave | plgPub | plgGet | plgSet | plgDel | plgNote
	plgFilterMask  = plgData | plgMeta | plgPres | plgInfo

	plgHandlerNames = []string{"hi", "acc", "login", "sub", "leave", "pub", "get", "set", "del", "note"}
	plgFilterNames  = []string{"data", "meta", "pres", "info"}
)

type PluginConfig struct {
	Enabled bool `json:"enabled"`
	// Unique service name
	Name string `json:"name"`
	// Microseconds to wait before timeout
	Timeout int64 `json:"timeout"`
	// Plugin type: filter or handler
	Type string `json:"type"`
	// List of message names this plugin triggers on
	MessageFilter []string `json:"message_filter"`
	// List of topics this plugin triggers on
	TopicFilter []string `json:"topic_filter"`
	// What should the server do if plugin failed: HTTP error code
	OnFailure int `json:"on_failure"`
	// Address of plugin server of the form "tcp://localhost:123" or "unix://path_to_socket_file"
	ServiceAddr string `json:"service_addr"`
}

type Plugin struct {
	name      string
	timeout   time.Duration
	isFilter  bool
	messages  uint32
	topics    []string
	onFailure int
	network   string
	addr      string
}

var plugins []Plugin

func pluginsInit(configString json.RawMessage) {
	// Check if any plugins are defined
	if configString == nil || len(configString) == 0 {
		return
	}

	var config []PluginConfig
	if err := json.Unmarshal(configString, &config); err != nil {
		log.Fatal(err)
	}

	plugins = make([]Plugin, len(config))
	count := 0
	for _, conf := range config {
		if !conf.Enabled {
			continue
		}

		names := plgFilterNames

		var msgFilter int32
		for _, msg := range conf.MessageFilter {
			idx := findInSlice(msg, names)
			if idx < 0 {
				log.Fatal("plugins: unknown message name", msg)
			}
			msgFilter |= 1 << idx
		}

		plugins[count] = Plugin{
			name:      conf.Name,
			timeout:   time.Duration(conf.Timeout * time.Microsecond),
			isFilter:  conf.Type == "filter",
			onFailure: conf.OnFailure,
			messages:  msgFilter,
			topics:    conf.TopicFilter,
			network:   "",
			addr:      "",
		}

		log.Println("Plugin defined:", conf.Name)
	}

	plugins = plugins[:count]
}

func plugnHandler(sess *Session, msg *ClientComMessage) *ServerComMessage {
	if plugins == nil {
		return nil
	}

	for _, p := range plugins {
		if p.isFilter {
			continue
		}
		if resp, err := p.Call(); res != nil {
			return res
		} else if err != nil {
			// Do something here
		}
	}

	return nil
}

func pluginFilter(msg *ServerComMessage) *ServerComMessage {

}

func findInSlice(needle string, haystack []string) int {
	for i, val := range haystack {
		if val == needle {
			return i
		}
	}
	return -1
}
