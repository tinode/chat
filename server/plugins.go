// External services contacted through RPC
package main

//go:generate protoc -I ../plugin --go_out=plugins=grpc:../plugin ../plugin/model.proto

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	pb "github.com/tinode/chat/plugin"
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

	req := pb.ClientReq{}
	// Convert ClientComMessage to a protobuf message
	if msg.Hi != nil {
		req.Message = &pb.ClientReq_Hi{Hi: &pb.ClientHi{
			UserAgent: msg.Hi.UserAgent,
			Ver:       int32(parseVersion(msg.Hi.Version)),
			DeviceId:  msg.Hi.DeviceID,
			Lang:      msg.Hi.Lang}}
	} else if msg.Acc != nil {
		acc := &pb.ClientAcc{
			Scheme: msg.Acc.Scheme,
			Secret: msg.Acc.Secret,
			Login:  msg.Acc.Login,
			Tags:   msg.Acc.Tags}
		if strings.HasPrefix(msg.Acc.User, "new") {
			acc.User = &pb.ClientAcc_IsNew{IsNew: true}
		} else {
			acc.User = &pb.ClientAcc_UserId{UserId: msg.Acc.User}
		}
		acc.Desc = pbSetDesc(msg.Acc.Desc)
		req.Message = &pb.ClientReq_Acc{Acc: acc}
	} else if msg.Login != nil {
		req.Message = &pb.ClientReq_Login{Login: &pb.ClientLogin{
			Scheme: msg.Login.Scheme,
			Secret: msg.Login.Secret}}
	} else if msg.Sub != nil {
		req.Message = &pb.ClientReq_Sub{Sub: &pb.ClientSub{
			Topic:    msg.Sub.Topic,
			SetQuery: pbSetQuery(msg.Sub.Set),
			GetQuery: pbGetQuery(msg.Sub.Get)}}
	} else if msg.Leave != nil {
		req.Message = &pb.ClientReq_Leave{Leave: &pb.ClientLeave{
			Topic: msg.Leave.Topic,
			Unsub: msg.Leave.Unsub}}
	} else if msg.Pub != nil {
		content, _ := json.Marshal(msg.Pub.Content)
		req.Message = &pb.ClientReq_Pub{Pub: &pb.ClientPub{
			Topic:   msg.Pub.Topic,
			NoEcho:  msg.Pub.NoEcho,
			Head:    msg.Pub.Head,
			Content: content}}
	} else if msg.Get != nil {
		req.Message = &pb.ClientReq_Get{Get: &pb.ClientGet{
			Topic: msg.Get.Topic,
			Query: pbGetQuery(&msg.Get.MsgGetQuery)}}
	} else if msg.Set != nil {
		req.Message = &pb.ClientReq_Set{Set: &pb.ClientSet{
			Topic: msg.Set.Topic,
			Query: pbSetQuery(&msg.Set.MsgSetQuery)}}
	} else if msg.Del != nil {
		var what pb.ClientDel_What
		switch msg.Del.What {
		case "msg":
			what = pb.ClientDel_MSG
		case "topic":
			what = pb.ClientDel_TOPIC
		case "sub":
			what = pb.ClientDel_SUB
		}
		req.Message = &pb.ClientReq_Del{Del: &pb.ClientDel{
			Topic:   msg.Del.Topic,
			What:    what,
			Before:  int32(msg.Del.Before),
			SeqList: intSliceToInt32(msg.Del.SeqList),
			UserId:  msg.Del.User,
			Hard:    msg.Del.Hard}}
	} else if msg.Note != nil {
		var what pb.InfoNote
		switch msg.Note.What {
		case "kp":
			what = pb.InfoNote_KP
		case "read":
			what = pb.InfoNote_READ
		case "recv":
			what = pb.InfoNote_RECV
		}
		req.Message = &pb.ClientReq_Note{Note: &pb.ClientNote{
			Topic: msg.Note.Topic,
			What:  what,
			SeqId: int32(msg.Note.SeqId)}}
	}

	// Add session as context
	req.Sess = &pb.Session{
		SessionId:  sess.sid,
		UserId:     sess.uid.UserId(),
		AuthLevel:  pb.Session_AuthLevel(sess.authLvl),
		UserAgent:  sess.userAgent,
		RemoteAddr: sess.remoteAddr,
		DeviceId:   sess.deviceId,
		Language:   sess.lang,
	}

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
			return &ServerComMessage{Ctrl: &MsgServerCtrl{
				Id:        id,
				Code:      p.failureCode,
				Text:      p.failureText,
				Topic:     topic,
				Timestamp: ts}}
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

func intSliceToInt32(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func pbGetQuery(in *MsgGetQuery) *pb.GetQuery {
	if in == nil {
		return nil
	}

	out := &pb.GetQuery{
		What: in.What,
	}

	convertTimeStamp := func(IfModifiedSince *time.Time) int64 {
		var ims int64
		if IfModifiedSince != nil {
			ims = IfModifiedSince.UnixNano() / 1000000
		}
		return ims
	}

	if in.Desc != nil {
		out.Desc = &pb.GetOpts{
			IfModifiedSince: convertTimeStamp(in.Desc.IfModifiedSince),
			Limit:           int32(in.Desc.Limit)}
	}
	if in.Sub != nil {
		out.Sub = &pb.GetOpts{
			IfModifiedSince: convertTimeStamp(in.Sub.IfModifiedSince),
			Limit:           int32(in.Sub.Limit)}
	}
	if in.Data != nil {
		out.Data = &pb.BrowseOpts{
			BeforeId: int32(in.Data.BeforeId),
			BeforeTs: convertTimeStamp(in.Data.BeforeTs),
			SinceId:  int32(in.Data.SinceId),
			SinceTs:  convertTimeStamp(in.Data.SinceTs),
			Limit:    int32(in.Data.Limit)}
	}
	return out
}

func pbSetDesc(in *MsgSetDesc) *pb.SetDesc {
	if in == nil {
		return nil
	}

	out := &pb.SetDesc{}
	if in.DefaultAcs != nil {
		out.DefaultAcs = &pb.DefaultAcsMode{
			Auth: in.DefaultAcs.Auth,
			Anon: in.DefaultAcs.Anon}
	}
	if in.Public != nil {
		out.Public, _ = json.Marshal(in.Public)
	}
	if in.Private != nil {
		out.Private, _ = json.Marshal(in.Private)
	}
	return out
}

func pbSetQuery(in *MsgSetQuery) *pb.SetQuery {
	if in == nil {
		return nil
	}

	out := &pb.SetQuery{
		Desc: pbSetDesc(in.Desc),
	}

	if in.Sub != nil {
		out.Sub = &pb.SetSub{
			UserId: in.Sub.User,
			Mode:   in.Sub.Mode,
		}
	}
	return out
}
