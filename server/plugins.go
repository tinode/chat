// External services contacted through RPC
package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	pb "github.com/tinode/chat/plugin"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

//go:generate protoc -I ../plugin --go_out=plugins=grpc:../plugin ../plugin/model.proto

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

	plgData = 1 << iota
	plgMeta
	plgPres
	plgInfo

	plgHandlerMask = plgHi | plgAcc | plgLogin | plgSub | plgLeave | plgPub | plgGet | plgSet | plgDel | plgNote
	plgFilterMask  = plgData | plgMeta | plgPres | plgInfo
)

var (
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

	conn   *grpc.ClientConn
	client pb.PluginClient
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

	nameIndex := make(map[string]bool)
	plugins = make([]Plugin, len(config))
	count := 0
	for _, conf := range config {
		if !conf.Enabled {
			continue
		}

		if nameIndex[conf.Name] {
			log.Fatalf("plugins: duplicate name '%s'", conf.Name)
		}

		var names []string
		var mask uint32
		if conf.Type == "filter" {
			names = plgFilterNames
			mask = plgFilterMask
		} else {
			names = plgHandlerNames
			mask = plgHandlerMask
		}

		var msgFilter uint32
		if conf.MessageFilter == nil || (len(conf.MessageFilter) == 1 && conf.MessageFilter[0] == "*") {
			msgFilter = mask
		} else {
			for _, msg := range conf.MessageFilter {
				idx := findInSlice(msg, names)
				if idx < 0 {
					log.Fatalf("plugins: unknown message name '%s'", msg)
				}
				msgFilter |= (1 << uint(idx))
			}
		}

		if msgFilter == 0 {
			continue
		}

		plugins[count] = Plugin{
			name:      conf.Name,
			timeout:   time.Duration(conf.Timeout) * time.Microsecond,
			isFilter:  conf.Type == "filter",
			onFailure: conf.OnFailure,
			messages:  msgFilter,
			topics:    conf.TopicFilter,
		}

		if parts := strings.SplitN(conf.ServiceAddr, "://", 2); len(parts) < 2 {
			log.Fatal("plugins: invalid server address format", conf.ServiceAddr)
		} else {
			plugins[count].network = parts[0]
			plugins[count].addr = parts[1]
		}

		var err error
		plugins[count].conn, err = grpc.Dial(plugins[count].addr, grpc.WithInsecure())
		if err != nil {
			log.Fatalf("plugins: connection failure %v", err)
		}

		plugins[count].client = pb.NewPluginClient(plugins[count].conn)

		nameIndex[conf.Name] = true
		count++
	}

	plugins = plugins[:count]
	if len(plugins) == 0 {
		log.Fatal("plugins: no active plugins found")
	}

	var names []string
	for _, plg := range plugins {
		names = append(names, plg.name)
	}

	log.Println("plugins: active", "'"+strings.Join(names, "', '")+"'")
}

func pluginsShutdown() {
	if plugins == nil {
		return
	}

	for _, p := range plugins {
		p.conn.Close()
	}
}

func plugnHandler(sess *Session, msg *ClientComMessage) *ServerComMessage {
	if plugins == nil {
		return nil
	}

	req := pb.ClientMsg{}
	var id string
	var topic string
	ts := time.Now().UTC().Round(time.Millisecond)
	for _, p := range plugins {
		if p.isFilter {
			continue
		}

		if resp, err := p.client.HandleMessage(context.Background(), &req); err == nil {
			// Response code 0 means default processing
			if resp.GetCode() == 0 {
				return nil
			}

			return &ServerComMessage{Ctrl: &MsgServerCtrl{
				Id:        id,
				Code:      int(resp.GetCode()),
				Text:      resp.GetText(),
				Topic:     topic,
				Timestamp: ts}}
		} else if err != nil {
			// Do something here
		}
	}

	return nil
}

func pluginFilter(msg *ServerComMessage) *ServerComMessage {
	return nil
}

func findInSlice(needle string, haystack []string) int {
	for i, val := range haystack {
		if val == needle {
			return i
		}
	}
	return -1
}
