// External services contacted through RPC
package main

import (
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/store/types"
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
	plgData
	plgMeta
	plgPres
	plgInfo

	plgClientMask = plgHi | plgAcc | plgLogin | plgSub | plgLeave | plgPub | plgGet | plgSet | plgDel | plgNote
	plgServerMask = plgData | plgMeta | plgPres | plgInfo

	plgActCreate = 1 << iota
	plgActUpd
	plgActDel

	plgActMask = plgActCreate | plgActUpd | plgActDel

	plgTopicMe = 1 << iota
	plgTopicFnd
	plgTopicP2P
	plgTopicGrp
	plgTopicNew

	plgTopicCatMask = plgTopicMe | plgTopicFnd | plgTopicP2P | plgTopicGrp

	plgFilterByTopicType = 1 << iota
	plgFilterByPacket
	plgFilterByAction
)

var (
	plgPacketNames = []string{
		"hi", "acc", "login", "sub", "leave", "pub", "get", "set", "del", "note",
		"data", "meta", "pres", "info",
	}

	plgTopicCatNames = []string{"me", "fnd", "p2p", "grp", "new"}
)

type PluginFilter struct {
	byPacket    int
	byTopicType int
	byAction    int
}

func ParsePluginFilter(s *string, filterBy int) (*PluginFilter, error) {
	if s == nil {
		return nil, nil
	}

	parseByName := func(parts []string, options []string, def int) (int, error) {
		var result int

		// Iterate over filter parts
		for _, inp := range parts {
			if inp != "" {
				inp = strings.ToLower(inp)
				// Split string like "hi,login,pres" or "me,p2p,fnd"
				values := strings.Split(inp, ",")
				// For each value in the input string, try to find it in the options set
				for _, val := range values {
					i := 0
					// Iterate over the options, i.e find "hi" in the slice of packet names
					for i = range options {
						if options[i] == val {
							result |= 1 << uint(i)
							break
						}
					}

					if result != 0 && i == len(options) {
						// Mix of known and unknown options in the input
						return 0, errors.New("plugin: unknown value in filter " + val)
					}
				}

				if result != 0 {
					// Found and parsed the right part
					break
				}
			}
		}

		// If the filter value is not defined, use default.
		if result == 0 {
			result = def
		}

		return result, nil
	}

	parseAction := func(parts []string) int {
		var result int
		for _, inp := range parts {

			for _, char := range inp {
				switch char {
				case 'c', 'C':
					result |= plgActCreate
				case 'u', 'U':
					result |= plgActUpd
				case 'd', 'D':
					result |= plgActDel
				default:
					// Unknown symbol means this is not an action string.
					result = 0
					break
				}
			}

			if result != 0 {
				// Found and parsed actions.
				break
			}
		}
		if result == 0 {
			result = plgActMask
		}
		return result
	}

	filter := PluginFilter{}
	parts := strings.Split(*s, ";")
	var err error

	if filterBy&plgFilterByPacket != 0 {
		if filter.byPacket, err = parseByName(parts, plgPacketNames, plgClientMask); err != nil {
			return nil, err
		}
	}

	if filterBy&plgFilterByTopicType != 0 {
		if filter.byTopicType, err = parseByName(parts, plgTopicCatNames, plgTopicCatMask); err != nil {
			return nil, err
		}
	}

	if filterBy&plgFilterByAction != 0 {
		filter.byAction = parseAction(parts)
	}

	return &filter, nil
}

// Filters for an individual RPC call. Filter strings are formatted as follows:
// <comma separated list of packet names> : <comma separated list of topics or topic types> : <actions (combination of C U D)>
// For instance:
// "acc,login::CU" - grab packets {acc} or {login}; no filtering by topic, Create or Update action
// "pub,pres:me,p2p:"
type PluginRPCFilterConfig struct {
	// Filter by packet name, topic type [or exact name - not supported yet]. 2D: "pub,pres;p2p,me"
	FireHose *string `json:"fire_hose"`
	// Filter by CUD, [exact user name - not supported yet]. 1D: "C"
	Account *string `json:"account"`
	// Filter by CUD, topic type[, exact name]: "p2p;CU"
	Topic *string `json:"topic"`
	// Filter by CUD, topic type[, exact topic name, exact user name]: "CU"
	Subscription *string `json:"subscription"`
	// Filter by C.D, topic type[, exact topic name, exact user name]: "grp;CD"
	Message *string `json:"message"`
}

type PluginConfig struct {
	Enabled bool `json:"enabled"`
	// Unique service name
	Name string `json:"name"`
	// Microseconds to wait before timeout
	Timeout int64 `json:"timeout"`
	// Filters for RPC calls: when to call vs when to skip the call
	Filters PluginRPCFilterConfig `json:"filters"`
	// What should the server do if plugin failed: HTTP error code
	FailureCode int `json:"failure_code"`
	// HTTP Error message to go with the code
	FailureMessage string `json:"failure_text"`
	// Address of plugin server of the form "tcp://localhost:123" or "unix://path_to_socket_file"
	ServiceAddr string `json:"service_addr"`
}

type Plugin struct {
	name    string
	timeout time.Duration
	// Filters for individual methods
	filterFireHose     *PluginFilter
	filterAccount      *PluginFilter
	filterTopic        *PluginFilter
	filterSubscription *PluginFilter
	filterMessage      *PluginFilter
	failureCode        int
	failureText        string
	network            string
	addr               string

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

		plugins[count] = Plugin{
			name:        conf.Name,
			timeout:     time.Duration(conf.Timeout) * time.Microsecond,
			failureCode: conf.FailureCode,
			failureText: conf.FailureMessage,
		}
		var err error
		if plugins[count].filterFireHose, err =
			ParsePluginFilter(conf.Filters.FireHose, plgFilterByTopicType|plgFilterByPacket); err != nil {
			log.Fatal("plugins: bad FireHose filter", err)
		}
		if plugins[count].filterAccount, err =
			ParsePluginFilter(conf.Filters.Account, plgFilterByAction); err != nil {
			log.Fatal("plugins: bad Account filter", err)
		}
		if plugins[count].filterTopic, err =
			ParsePluginFilter(conf.Filters.Topic, plgFilterByTopicType|plgFilterByAction); err != nil {
			log.Fatal("plugins: bad FireHose filter", err)
		}
		if plugins[count].filterSubscription, err =
			ParsePluginFilter(conf.Filters.Subscription, plgFilterByTopicType|plgFilterByAction); err != nil {
			log.Fatal("plugins: bad Subscription filter", err)
		}
		if plugins[count].filterMessage, err =
			ParsePluginFilter(conf.Filters.Message, plgFilterByTopicType|plgFilterByAction); err != nil {
			log.Fatal("plugins: bad Message filter", err)
		}

		if parts := strings.SplitN(conf.ServiceAddr, "://", 2); len(parts) < 2 {
			log.Fatal("plugins: invalid server address format", conf.ServiceAddr)
		} else {
			plugins[count].network = parts[0]
			plugins[count].addr = parts[1]
		}

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

func pluginFireHose(sess *Session, msg *ClientComMessage) (*ClientComMessage, *ServerComMessage) {
	if plugins == nil {
		// Return the original message to continue processing without changes
		return msg, nil
	}

	var req *pbx.ClientReq

	var id string
	var topic string
	ts := time.Now().UTC().Round(time.Millisecond)
	for _, p := range plugins {
		if !pluginDoFiltering(p.filterFireHose, msg) {
			// Plugin is not interested in FireHose
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
		if resp, err := p.client.FireHose(ctx, req); err == nil {
			respStatus := resp.GetStatus()
			// CONTINUE means default processing
			if respStatus == pbx.ServerResp_CONTINUE {
				continue
			}
			// DROP means stop processing of the message
			if respStatus == pbx.ServerResp_DROP {
				return nil, nil
			}
			// REPLACE: ClientMsg was updated by the plugin. Use the new one for further processing.
			if respStatus == pbx.ServerResp_REPLACE {
				return pb_cli_deserialize(resp.GetClmsg()), nil
			}
			// RESPOND: Plugin provided an alternative response message. Use it
			return nil, pb_serv_deserialize(resp.GetSrvmsg())

		} else if p.failureCode != 0 {
			// Plugin failed and it's configured to stop futher processing.
			log.Println("plugin: failed,", p.name, err)
			return nil, &ServerComMessage{Ctrl: &MsgServerCtrl{
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

	return msg, nil
}

func pluginAccount(user *types.User, action int) {
	if plugins == nil {
		return
	}

	var event *pbx.AccountEvent
	for _, p := range plugins {
		if p.filterAccount == nil || p.filterAccount.byAction&action == 0 {
			// Plugin is not interested in Account actions
			continue
		}

		if event == nil {
			event = &pbx.AccountEvent{
				Action: pluginActionToCrud(action),
				UserId: user.Uid().UserId(),
				DefaultAcs: pb_DefaultAcs_serialize(&MsgDefaultAcsMode{
					Auth: user.Access.Auth.String(),
					Anon: user.Access.Anon.String()}),
				Public: interfaceToBytes(user.Public),
				Tags:   user.Tags,
			}
		}

		var ctx context.Context
		var cancel context.CancelFunc
		if p.timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), p.timeout)
			defer cancel()
		} else {
			ctx = context.Background()
		}
		if _, err := p.client.Account(ctx, event); err != nil {
			log.Println("plugins: Account call failed", p.name, err)
		}
	}
}

func pluginTopic(msg *ServerComMessage) *ServerComMessage {
	return nil
}

func pluginSubscription(msg *ServerComMessage) *ServerComMessage {
	return nil
}

func pluginMessage(msg *ServerComMessage) *ServerComMessage {
	return nil
}

// Returns false to skip, true to process
func pluginDoFiltering(filter *PluginFilter, msg *ClientComMessage) bool {
	filterByTopic := func(topic string, flt int) bool {
		if topic == "" || flt == plgTopicCatMask {
			return true
		}
		switch topic[:3] {
		case "me":
			return flt&plgTopicMe != 0
		case "fnd":
			return flt&plgTopicFnd != 0
		case "usr":
			return flt&plgTopicP2P != 0
		case "grp":
			return flt&plgTopicGrp != 0
		case "new":
			return flt&plgTopicNew != 0
		}
		return false
	}

	// Check if plugin has any filters for this call
	if filter == nil || filter.byPacket == 0 {
		return false
	}
	// Check if plugin wants all the messages
	if filter.byPacket == plgClientMask && filter.byTopicType == plgTopicCatMask {
		return true
	}
	// Check individual bits
	if msg.Hi != nil {
		return filter.byPacket&plgHi != 0
	}
	if msg.Acc != nil {
		return filter.byPacket&plgAcc != 0
	}
	if msg.Login != nil {
		return filter.byPacket&plgLogin != 0
	}
	if msg.Sub != nil {
		return filter.byPacket&plgSub != 0 && filterByTopic(msg.Sub.Topic, filter.byTopicType)
	}
	if msg.Leave != nil {
		return filter.byPacket&plgLeave != 0 && filterByTopic(msg.Leave.Topic, filter.byTopicType)
	}
	if msg.Pub != nil {
		return filter.byPacket&plgPub != 0 && filterByTopic(msg.Pub.Topic, filter.byTopicType)
	}
	if msg.Get != nil {
		return filter.byPacket&plgGet != 0 && filterByTopic(msg.Get.Topic, filter.byTopicType)
	}
	if msg.Set != nil {
		return filter.byPacket&plgSet != 0 && filterByTopic(msg.Set.Topic, filter.byTopicType)
	}
	if msg.Del != nil {
		return filter.byPacket&plgDel != 0 && filterByTopic(msg.Del.Topic, filter.byTopicType)
	}
	if msg.Note != nil {
		return filter.byPacket&plgNote != 0 && filterByTopic(msg.Note.Topic, filter.byTopicType)
	}
	return false
}

func pluginActionToCrud(action int) pbx.Crud {
	switch action {
	case plgActCreate:
		return pbx.Crud_CREATE
	case plgActUpd:
		return pbx.Crud_UPDATE
	case plgActDel:
		return pbx.Crud_DELETE
	}
	panic("plugin: unknown action")
}
