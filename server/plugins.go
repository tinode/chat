// External services contacted through RPC
package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/tinode/chat/pbx"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
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

	plgData = 1 << iota
	plgMeta
	plgPres
	plgInfo

	plgClientMask = plgHi | plgAcc | plgLogin | plgSub | plgLeave | plgPub | plgGet | plgSet | plgDel | plgNote
	plgServerMask = plgData | plgMeta | plgPres | plgInfo
)

const (
	plgActCreate = 1 << iota
	plgActUpd
	plgActDel
)

var (
	plgClientNames = []string{"hi", "acc", "login", "sub", "leave", "pub", "get", "set", "del", "note"}
	plgServerNames = []string{"data", "meta", "pres", "info"}
)

type PluginFilter struct {
	packet int
	topic  []string
	action int
}

// Filters for individual RPC call. Filter strings are formatted as follows:
// <comma separated list of packet names> : <list of comma separated topics or topic types> : [CUD]
// For instance:
// "acc,login::CU" - grab packets {acc} or {login}, any topic (no filtering), Create or Update action
// "pub
type PluginRPCFilterConfig struct {
	FireHose     *string // Filter by topic type, exact name, packet type.
	Account      *string // Filter by CUD, exact user name, maybe AuthLevel?
	Topic        *string // Filter by CUD, topic type, exact name
	Subscription *string // Filter by CUD, topic type, exact topic name, exact user name
	Message      *string // Filter by C.D, topic type, exact topic name, exact user name
}

type PluginConfig struct {
	Enabled bool `json:"enabled"`
	// Unique service name
	Name string `json:"name"`
	// Microseconds to wait before timeout
	Timeout int64 `json:"timeout"`
	// Filters for RPC calls: when to call vs when to skip the call
	Filters PluginEventFilterConfig `json:"filters"`
	// What should the server do if plugin failed: HTTP error code
	FailureCode int `json:"failure_code"`
	// HTTP Error message to go with the code
	FailureMessage string `json:"failure_text"`
	// Address of plugin server of the form "tcp://localhost:123" or "unix://path_to_socket_file"
	ServiceAddr string `json:"service_addr"`
}

type Plugin struct {
	name        string
	timeout     time.Duration
	isFilter    bool
	messages    uint32
	topics      []string
	failureCode int
	failureText string
	network     string
	addr        string

	conn   *grpc.ClientConn
	client pbx.PluginClient
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
			name:        conf.Name,
			timeout:     time.Duration(conf.Timeout) * time.Microsecond,
			isFilter:    conf.Type == "filter",
			failureCode: conf.FailureCode,
			failureText: conf.FailureMessage,
			messages:    msgFilter,
			topics:      conf.TopicFilter,
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

		plugins[count].client = pbx.NewPluginClient(plugins[count].conn)

		nameIndex[conf.Name] = true
		count++
	}

	plugins = plugins[:count]
	if len(plugins) == 0 {
		log.Println("plugins: no active plugins found")
		plugins = nil
	} else {
		var names []string
		for _, plg := range plugins {
			names = append(names, plg.name+"("+plg.addr+")")
		}

		log.Println("plugins: active", "'"+strings.Join(names, "', '")+"'")
	}
}

func pluginsShutdown() {
	if plugins == nil {
		return
	}

	for _, p := range plugins {
		p.conn.Close()
	}
}

func pluginGenerateClientReq(sess *Session, msg *ClientComMessage) *pbx.ClientReq {
	return &pbx.ClientReq{
		Msg: pb_cli_serialize(msg),
		Sess: &pbx.Session{
			SessionId:  sess.sid,
			UserId:     sess.uid.UserId(),
			AuthLevel:  pbx.Session_AuthLevel(sess.authLvl),
			UserAgent:  sess.userAgent,
			RemoteAddr: sess.remoteAddr,
			DeviceId:   sess.deviceId,
			Language:   sess.lang}}
}

func pluginHandler(sess *Session, msg *ClientComMessage) *ServerComMessage {
	if plugins == nil {
		return nil
	}

	var req *pbx.ClientReq

	var id string
	var topic string
	ts := time.Now().UTC().Round(time.Millisecond)
	for _, p := range plugins {
		if p.isFilter {
			continue
		}

		if req == nil {
			// Generate request only if needed
			req = pluginGenerateClientReq(sess, msg)
		}

		var ctx context.Context
		var cancel context.CancelFunc
		if p.timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), p.timeout)
			defer cancel()
		} else {
			ctx = context.Background()
		}
		if resp, err := p.client.ClientMessage(ctx, req); err == nil {
			// Response code 0 means default processing
			if resp.GetCode() == 0 {
				continue
			}

			// This plugin returned non-zero. Subsequent plugins wil not be called.
			return &ServerComMessage{Ctrl: &MsgServerCtrl{
				Id:        id,
				Code:      int(resp.GetCode()),
				Text:      resp.GetText(),
				Topic:     topic,
				Timestamp: ts}}
		} else if p.failureCode != 0 {
			// Plugin failed and it's configured to stop futher processing.
			log.Println("plugin: failed,", p.name, err)
			return &ServerComMessage{Ctrl: &MsgServerCtrl{
				Id:        id,
				Code:      p.failureCode,
				Text:      p.failureText,
				Topic:     topic,
				Timestamp: ts}}
		} else {
			// Plugin failed but configured to ignore failure.
			log.Println("plugin: failure ignored,", p.name, err)
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
