// Converts between protobuf structs and Go representation of packets

package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/tinode/chat/pbx"
)

// Convert ServerComMessage to pbx.ServerMsg
func pb_serv_serialize(msg *ServerComMessage) *pbx.ServerMsg {
	var pkt pbx.ServerMsg

	if msg.Ctrl != nil {
		var params map[string][]byte
		if msg.Ctrl.Params != nil {
			if in, ok := msg.Ctrl.Params.(map[string]interface{}); ok {
				params = interfaceMapToByteMap(in)
			}
		}
		pkt.Message = &pbx.ServerMsg_Ctrl{Ctrl: &pbx.ServerCtrl{
			Id:     msg.Ctrl.Id,
			Topic:  msg.Ctrl.Topic,
			Code:   int32(msg.Ctrl.Code),
			Text:   msg.Ctrl.Text,
			Params: params}}
	} else if msg.Data != nil {
		pkt.Message = &pbx.ServerMsg_Data{Data: &pbx.ServerData{
			Topic:      msg.Data.Topic,
			FromUserId: msg.Data.From,
			DeletedAt:  timeToInt64(msg.Data.DeletedAt),
			SeqId:      int32(msg.Data.SeqId),
			Head:       msg.Data.Head,
			Content:    interfaceToBytes(msg.Data.Content)}}
	} else if msg.Pres != nil {
		var what pbx.ServerPres_What
		switch msg.Pres.What {
		case "on":
			what = pbx.ServerPres_ON
		case "off":
			what = pbx.ServerPres_OFF
		case "ua":
			what = pbx.ServerPres_UA
		case "upd":
			what = pbx.ServerPres_UPD
		case "gone":
			what = pbx.ServerPres_GONE
		case "acs":
			what = pbx.ServerPres_ACS
		case "term":
			what = pbx.ServerPres_TERM
		case "msg":
			what = pbx.ServerPres_MSG
		case "read":
			what = pbx.ServerPres_READ
		case "recv":
			what = pbx.ServerPres_RECV
		case "del":
			what = pbx.ServerPres_DEL
		default:
			log.Fatal("Unknown pres.what value", msg.Pres.What)
		}
		pkt.Message = &pbx.ServerMsg_Pres{Pres: &pbx.ServerPres{
			Topic:        msg.Pres.Topic,
			Src:          msg.Pres.Src,
			What:         what,
			UserAgent:    msg.Pres.UserAgent,
			SeqId:        int32(msg.Pres.SeqId),
			DelId:        int32(msg.Pres.DelId),
			DelSeq:       pb_DelQuery_serialize(msg.Pres.DelSeq),
			TargetUserId: msg.Pres.AcsTarget,
			ActorUserId:  msg.Pres.AcsActor,
			Acs:          pb_AccessMode_serialize(msg.Pres.Acs)}}
	} else if msg.Info != nil {
		pkt.Message = &pbx.ServerMsg_Info{Info: &pbx.ServerInfo{
			Topic:      msg.Info.Topic,
			FromUserId: msg.Info.From,
			What:       pb_InfoNoteWhat_serialize(msg.Info.What),
			SeqId:      int32(msg.Info.SeqId),
		}}
	} else if msg.Meta != nil {
		pkt.Message = &pbx.ServerMsg_Meta{Meta: &pbx.ServerMeta{
			Id:    msg.Meta.Id,
			Topic: msg.Meta.Topic,
			Desc:  pb_TopicDesc_serialize(msg.Meta.Desc),
			Sub:   pb_TopicSubSlice_serialize(msg.Meta.Sub),
			Del:   pb_DelValues_serialize(msg.Meta.Del),
		}}
	}

	return &pkt
}

func pb_serv_deserialize(pkt *pbx.ServerMsg) *ServerComMessage {
	var msg ServerComMessage
	if ctrl := pkt.GetCtrl(); ctrl != nil {
		msg.Ctrl = &MsgServerCtrl{
			Id:     ctrl.GetId(),
			Topic:  ctrl.GetTopic(),
			Code:   int(ctrl.GetCode()),
			Text:   ctrl.GetText(),
			Params: byteMapToInterfaceMap(ctrl.GetParams()),
		}
	} else if data := pkt.GetData(); data != nil {
		msg.Data = &MsgServerData{
			Topic:     data.GetTopic(),
			From:      data.GetFromUserId(),
			DeletedAt: int64ToTime(data.GetDeletedAt()),
			SeqId:     int(data.GetSeqId()),
			Head:      data.GetHead(),
			Content:   data.GetContent(),
		}
	} else if pres := pkt.GetPres(); pres != nil {
		var what string
		switch pres.GetWhat() {
		case pbx.ServerPres_ON:
			what = "on"
		case pbx.ServerPres_OFF:
			what = "off"
		case pbx.ServerPres_UA:
			what = "ua"
		case pbx.ServerPres_UPD:
			what = "upd"
		case pbx.ServerPres_GONE:
			what = "gone"
		case pbx.ServerPres_ACS:
			what = "acs"
		case pbx.ServerPres_TERM:
			what = "term"
		case pbx.ServerPres_MSG:
			what = "msg"
		case pbx.ServerPres_READ:
			what = "read"
		case pbx.ServerPres_RECV:
			what = "recv"
		case pbx.ServerPres_DEL:
			what = "del"
		}
		msg.Pres = &MsgServerPres{
			Topic:     pres.GetTopic(),
			Src:       pres.GetSrc(),
			What:      what,
			UserAgent: pres.GetUserAgent(),
			SeqId:     int(pres.GetSeqId()),
			DelId:     int(pres.GetDelId()),
			DelSeq:    pb_DelQuery_deserialize(pres.GetDelSeq()),
			AcsTarget: pres.GetTargetUserId(),
			AcsActor:  pres.GetActorUserId(),
			Acs:       pb_AccessMode_deserialize(pres.GetAcs()),
		}
	} else if info := pkt.GetInfo(); info != nil {
		msg.Info = &MsgServerInfo{
			Topic: info.GetTopic(),
			From:  info.GetFromUserId(),
			What:  pb_InfoNoteWhat_deserialize(info.GetWhat()),
			SeqId: int(info.GetSeqId()),
		}
	} else if meta := pkt.GetMeta(); meta != nil {
		msg.Meta = &MsgServerMeta{
			Id:    meta.GetId(),
			Topic: meta.GetTopic(),
			Desc:  pb_TopicDesc_deserialize(meta.GetDesc()),
			Sub:   pb_TopicSubSlice_deserialize(meta.GetSub()),
			Del:   pb_DelValues_deserialize(meta.GetDel()),
		}
	}
	return &msg
}

// Convert ClientComMessage to pbx.ClientMsg
func pb_cli_serialize(msg *ClientComMessage) *pbx.ClientMsg {
	var pkt pbx.ClientMsg

	if msg.Hi != nil {
		pkt.Message = &pbx.ClientMsg_Hi{Hi: &pbx.ClientHi{
			Id:        msg.Hi.Id,
			UserAgent: msg.Hi.UserAgent,
			Ver:       msg.Hi.Version,
			DeviceId:  msg.Hi.DeviceID,
			Lang:      msg.Hi.Lang}}
	} else if msg.Acc != nil {
		acc := &pbx.ClientAcc{
			Id:     msg.Acc.Id,
			UserId: msg.Acc.User,
			Scheme: msg.Acc.Scheme,
			Secret: msg.Acc.Secret,
			Login:  msg.Acc.Login,
			Tags:   msg.Acc.Tags}
		acc.Desc = pb_SetDesc_serialize(msg.Acc.Desc)
		pkt.Message = &pbx.ClientMsg_Acc{Acc: acc}
	} else if msg.Login != nil {
		pkt.Message = &pbx.ClientMsg_Login{Login: &pbx.ClientLogin{
			Id:     msg.Login.Id,
			Scheme: msg.Login.Scheme,
			Secret: msg.Login.Secret}}
	} else if msg.Sub != nil {
		pkt.Message = &pbx.ClientMsg_Sub{Sub: &pbx.ClientSub{
			Id:       msg.Sub.Id,
			Topic:    msg.Sub.Topic,
			SetQuery: pb_SetQuery_serialize(msg.Sub.Set),
			GetQuery: pb_GetQuery_serialize(msg.Sub.Get)}}
	} else if msg.Leave != nil {
		pkt.Message = &pbx.ClientMsg_Leave{Leave: &pbx.ClientLeave{
			Id:    msg.Leave.Id,
			Topic: msg.Leave.Topic,
			Unsub: msg.Leave.Unsub}}
	} else if msg.Pub != nil {
		pkt.Message = &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{
			Id:      msg.Pub.Id,
			Topic:   msg.Pub.Topic,
			NoEcho:  msg.Pub.NoEcho,
			Head:    msg.Pub.Head,
			Content: interfaceToBytes(msg.Pub.Content)}}
	} else if msg.Get != nil {
		pkt.Message = &pbx.ClientMsg_Get{Get: &pbx.ClientGet{
			Id:    msg.Get.Id,
			Topic: msg.Get.Topic,
			Query: pb_GetQuery_serialize(&msg.Get.MsgGetQuery)}}
	} else if msg.Set != nil {
		pkt.Message = &pbx.ClientMsg_Set{Set: &pbx.ClientSet{
			Id:    msg.Set.Id,
			Topic: msg.Set.Topic,
			Query: pb_SetQuery_serialize(&msg.Set.MsgSetQuery)}}
	} else if msg.Del != nil {
		var what pbx.ClientDel_What
		switch msg.Del.What {
		case "msg":
			what = pbx.ClientDel_MSG
		case "topic":
			what = pbx.ClientDel_TOPIC
		case "sub":
			what = pbx.ClientDel_SUB
		}
		pkt.Message = &pbx.ClientMsg_Del{Del: &pbx.ClientDel{
			Id:     msg.Del.Id,
			Topic:  msg.Del.Topic,
			What:   what,
			DelSeq: pb_DelQuery_serialize(msg.Del.DelSeq),
			UserId: msg.Del.User,
			Hard:   msg.Del.Hard}}
	} else if msg.Note != nil {
		pkt.Message = &pbx.ClientMsg_Note{Note: &pbx.ClientNote{
			Topic: msg.Note.Topic,
			What:  pb_InfoNoteWhat_serialize(msg.Note.What),
			SeqId: int32(msg.Note.SeqId)}}
	}

	return &pkt
}

// Convert pbx.ClientMsg to ClientComMessage
func pb_cli_deserialize(pkt *pbx.ClientMsg) *ClientComMessage {
	var msg ClientComMessage
	if hi := pkt.GetHi(); hi != nil {
		msg.Hi = &MsgClientHi{
			Id:        hi.GetId(),
			UserAgent: hi.GetUserAgent(),
			Version:   hi.GetVer(),
			DeviceID:  hi.GetDeviceId(),
			Lang:      hi.GetLang(),
		}
	} else if acc := pkt.GetAcc(); acc != nil {
		msg.Acc = &MsgClientAcc{
			Id:     acc.GetId(),
			User:   acc.GetUserId(),
			Scheme: acc.GetScheme(),
			Secret: acc.GetSecret(),
			Login:  acc.GetLogin(),
			Tags:   acc.GetTags(),
			Desc:   pb_SetDesc_deserialize(acc.GetDesc()),
		}
	} else if login := pkt.GetLogin(); login != nil {
		msg.Login = &MsgClientLogin{
			Id:     login.GetId(),
			Scheme: login.GetScheme(),
			Secret: login.GetSecret(),
		}
	} else if sub := pkt.GetSub(); sub != nil {
		msg.Sub = &MsgClientSub{
			Id:    sub.GetId(),
			Topic: sub.GetTopic(),
			Get:   pb_GetQuery_deserialize(sub.GetGetQuery()),
			Set:   pb_SetQuery_deserialize(sub.GetSetQuery()),
		}
	} else if leave := pkt.GetLeave(); leave != nil {
		msg.Leave = &MsgClientLeave{
			Id:    leave.GetId(),
			Topic: leave.GetTopic(),
			Unsub: leave.GetUnsub(),
		}
	} else if pub := pkt.GetPub(); pub != nil {
		msg.Pub = &MsgClientPub{
			Id:      pub.GetId(),
			Topic:   pub.GetTopic(),
			NoEcho:  pub.GetNoEcho(),
			Head:    pub.GetHead(),
			Content: bytesToInterface(pub.GetContent()),
		}
	} else if get := pkt.GetGet(); get != nil {
		msg.Get = &MsgClientGet{
			Id:          get.GetId(),
			Topic:       get.GetTopic(),
			MsgGetQuery: *pb_GetQuery_deserialize(get.GetQuery()),
		}
	} else if set := pkt.GetSet(); set != nil {
		msg.Set = &MsgClientSet{
			Id:          set.GetId(),
			Topic:       set.GetTopic(),
			MsgSetQuery: *pb_SetQuery_deserialize(set.GetQuery()),
		}
	} else if del := pkt.GetDel(); del != nil {
		msg.Del = &MsgClientDel{
			Id:     del.GetId(),
			Topic:  del.GetTopic(),
			DelSeq: pb_DelQuery_deserialize(del.GetDelSeq()),
			User:   del.GetUserId(),
			Hard:   del.GetHard(),
		}
		switch del.GetWhat() {
		case pbx.ClientDel_MSG:
			msg.Del.What = "msg"
		case pbx.ClientDel_TOPIC:
			msg.Del.What = "topic"
		case pbx.ClientDel_SUB:
			msg.Del.What = "sub"
		}
	} else if note := pkt.GetNote(); note != nil {
		msg.Note = &MsgClientNote{
			Topic: note.GetTopic(),
			SeqId: int(note.GetSeqId()),
		}
		switch note.GetWhat() {
		case pbx.InfoNote_READ:
			msg.Note.What = "read"
		case pbx.InfoNote_RECV:
			msg.Note.What = "recv"
		case pbx.InfoNote_KP:
			msg.Note.What = "kp"
		}
	}
	return &msg
}

func interfaceMapToByteMap(in map[string]interface{}) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for key, val := range in {
		out[key], _ = json.Marshal(val)
	}
	return out
}

func byteMapToInterfaceMap(in map[string][]byte) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, val := range in {
		var iface interface{}
		json.Unmarshal(val, &iface)
		out[key] = iface
	}
	return out
}

func interfaceToBytes(in interface{}) []byte {
	out, _ := json.Marshal(in)
	return out
}

func bytesToInterface(in []byte) interface{} {
	var out interface{}
	json.Unmarshal(in, &out)
	return out
}

func intSliceToInt32(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func int32SliceToInt(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

func timeToInt64(ts *time.Time) int64 {
	if ts != nil {
		return ts.UnixNano() / int64(time.Millisecond)
	}
	return 0
}

func int64ToTime(ts int64) *time.Time {
	if ts > 0 {
		res := time.Unix(ts/1000, ts%1000).UTC()
		return &res
	}
	return nil
}

func pb_GetQuery_serialize(in *MsgGetQuery) *pbx.GetQuery {
	if in == nil {
		return nil
	}

	out := &pbx.GetQuery{
		What: in.What,
	}

	if in.Desc != nil {
		out.Desc = &pbx.GetOpts{
			IfModifiedSince: timeToInt64(in.Desc.IfModifiedSince),
			Limit:           int32(in.Desc.Limit)}
	}
	if in.Sub != nil {
		out.Sub = &pbx.GetOpts{
			IfModifiedSince: timeToInt64(in.Sub.IfModifiedSince),
			Limit:           int32(in.Sub.Limit)}
	}
	if in.Data != nil {
		out.Data = &pbx.BrowseOpts{
			BeforeId: int32(in.Data.BeforeId),
			SinceId:  int32(in.Data.SinceId),
			Limit:    int32(in.Data.Limit)}
	}
	return out
}

func pb_GetQuery_deserialize(in *pbx.GetQuery) *MsgGetQuery {
	msg := MsgGetQuery{}

	if in != nil {
		msg.What = in.GetWhat()

		if desc := in.GetDesc(); desc != nil {
			msg.Desc = &MsgGetOpts{
				IfModifiedSince: int64ToTime(desc.GetIfModifiedSince()),
				Limit:           int(desc.GetLimit()),
			}
		}
		if sub := in.GetSub(); sub != nil {
			msg.Desc = &MsgGetOpts{
				IfModifiedSince: int64ToTime(sub.GetIfModifiedSince()),
				Limit:           int(sub.GetLimit()),
			}
		}
		if data := in.GetData(); data != nil {
			msg.Data = &MsgBrowseOpts{
				BeforeId: int(data.GetBeforeId()),
				SinceId:  int(data.GetSinceId()),
				Limit:    int(data.GetLimit()),
			}
		}
	}

	return &msg
}

func pb_SetDesc_serialize(in *MsgSetDesc) *pbx.SetDesc {
	if in == nil {
		return nil
	}

	return &pbx.SetDesc{
		DefaultAcs: pb_DefaultAcs_serialize(in.DefaultAcs),
		Public:     interfaceToBytes(in.Public),
		Private:    interfaceToBytes(in.Private),
	}
}

func pb_SetDesc_deserialize(in *pbx.SetDesc) *MsgSetDesc {
	if in == nil {
		return nil
	}

	return &MsgSetDesc{
		DefaultAcs: pb_DefaultAcs_deserialize(in.GetDefaultAcs()),
		Public:     bytesToInterface(in.GetPublic()),
		Private:    bytesToInterface(in.GetPrivate()),
	}
}

func pb_SetQuery_serialize(in *MsgSetQuery) *pbx.SetQuery {
	if in == nil {
		return nil
	}

	out := &pbx.SetQuery{
		Desc: pb_SetDesc_serialize(in.Desc),
	}

	if in.Sub != nil {
		out.Sub = &pbx.SetSub{
			UserId: in.Sub.User,
			Mode:   in.Sub.Mode,
		}
	}
	return out
}

func pb_SetQuery_deserialize(in *pbx.SetQuery) *MsgSetQuery {
	msg := MsgSetQuery{}

	if in != nil {
		if desc := in.GetDesc(); desc != nil {
			msg.Desc = pb_SetDesc_deserialize(desc)
		}
		if sub := in.GetSub(); sub != nil {
			msg.Sub = &MsgSetSub{
				User: sub.GetUserId(),
				Mode: sub.GetMode(),
			}
		}
	}

	return &msg
}

func pb_InfoNoteWhat_serialize(what string) pbx.InfoNote {
	var out pbx.InfoNote
	switch what {
	case "kp":
		out = pbx.InfoNote_KP
	case "read":
		out = pbx.InfoNote_READ
	case "recv":
		out = pbx.InfoNote_RECV
	default:
		log.Fatal("unknown info-note.what", what)
	}
	return out
}

func pb_InfoNoteWhat_deserialize(what pbx.InfoNote) string {
	var out string
	switch what {
	case pbx.InfoNote_KP:
		out = "kp"
	case pbx.InfoNote_READ:
		out = "read"
	case pbx.InfoNote_RECV:
		out = "recv"
	default:
		log.Fatal("unknown info-note.what", what)
	}
	return out
}

func pb_AccessMode_serialize(acs *MsgAccessMode) *pbx.AccessMode {
	if acs == nil {
		return nil
	}

	return &pbx.AccessMode{
		Want:  acs.Want,
		Given: acs.Given,
	}
}

func pb_AccessMode_deserialize(acs *pbx.AccessMode) *MsgAccessMode {
	if acs == nil {
		return nil
	}

	return &MsgAccessMode{
		Want:  acs.Want,
		Given: acs.Given,
	}
}

func pb_DefaultAcs_serialize(defacs *MsgDefaultAcsMode) *pbx.DefaultAcsMode {
	if defacs == nil {
		return nil
	}

	return &pbx.DefaultAcsMode{
		Auth: defacs.Auth,
		Anon: defacs.Anon}
}

func pb_DefaultAcs_deserialize(defacs *pbx.DefaultAcsMode) *MsgDefaultAcsMode {
	if defacs == nil {
		return nil
	}

	return &MsgDefaultAcsMode{
		Auth: defacs.GetAuth(),
		Anon: defacs.GetAnon(),
	}
}

func pb_TopicDesc_serialize(desc *MsgTopicDesc) *pbx.TopicDesc {
	if desc == nil {
		return nil
	}
	return &pbx.TopicDesc{
		CreatedAt: timeToInt64(desc.CreatedAt),
		UpdatedAt: timeToInt64(desc.UpdatedAt),
		Defacs:    pb_DefaultAcs_serialize(desc.DefaultAcs),
		Acs:       pb_AccessMode_serialize(desc.Acs),
		SeqId:     int32(desc.SeqId),
		ReadId:    int32(desc.ReadSeqId),
		RecvId:    int32(desc.RecvSeqId),
		DelId:     int32(desc.DelId),
		Public:    interfaceToBytes(desc.Public),
		Private:   interfaceToBytes(desc.Private),
	}
}

func pb_TopicDesc_deserialize(desc *pbx.TopicDesc) *MsgTopicDesc {
	if desc == nil {
		return nil
	}
	return &MsgTopicDesc{
		CreatedAt:  int64ToTime(desc.GetCreatedAt()),
		UpdatedAt:  int64ToTime(desc.GetUpdatedAt()),
		DefaultAcs: pb_DefaultAcs_deserialize(desc.GetDefacs()),
		Acs:        pb_AccessMode_deserialize(desc.GetAcs()),
		SeqId:      int(desc.SeqId),
		ReadSeqId:  int(desc.ReadId),
		RecvSeqId:  int(desc.RecvId),
		DelId:      int(desc.DelId),
		Public:     bytesToInterface(desc.Public),
		Private:    bytesToInterface(desc.Private),
	}
}

func pb_TopicSubSlice_serialize(subs []MsgTopicSub) []*pbx.TopicSub {
	if subs == nil || len(subs) == 0 {
		return nil
	}

	out := make([]*pbx.TopicSub, len(subs))
	for i := 0; i < len(subs); i++ {
		out[i] = &pbx.TopicSub{
			UpdatedAt: timeToInt64(subs[i].UpdatedAt),
			DeletedAt: timeToInt64(subs[i].DeletedAt),
			Online:    subs[i].Online,
			Acs:       pb_AccessMode_serialize(&subs[i].Acs),
			ReadId:    int32(subs[i].ReadSeqId),
			RecvId:    int32(subs[i].RecvSeqId),
			Public:    interfaceToBytes(subs[i].Public),
			Private:   interfaceToBytes(subs[i].Private),
			UserId:    subs[i].User,
			Topic:     subs[i].Topic,
			SeqId:     int32(subs[i].SeqId),
			DelId:     int32(subs[i].DelId),
		}
		if subs[i].LastSeen != nil {
			out[i].LastSeenTime = timeToInt64(subs[i].LastSeen.When)
			out[i].LastSeenUserAgent = subs[i].LastSeen.UserAgent
		}
	}
	return out
}

func pb_TopicSubSlice_deserialize(subs []*pbx.TopicSub) []MsgTopicSub {
	if subs == nil || len(subs) == 0 {
		return nil
	}

	out := make([]MsgTopicSub, len(subs))
	for i := 0; i < len(subs); i++ {
		out[i] = MsgTopicSub{
			UpdatedAt: int64ToTime(subs[i].GetUpdatedAt()),
			DeletedAt: int64ToTime(subs[i].GetDeletedAt()),
			Online:    subs[i].GetOnline(),
			ReadSeqId: int(subs[i].GetReadId()),
			RecvSeqId: int(subs[i].GetRecvId()),
			Public:    bytesToInterface(subs[i].GetPublic()),
			Private:   bytesToInterface(subs[i].GetPrivate()),
			User:      subs[i].GetUserId(),
			Topic:     subs[i].GetTopic(),
			SeqId:     int(subs[i].GetSeqId()),
			DelId:     int(subs[i].GetDelId()),
		}
		if acs := subs[i].GetAcs(); acs != nil {
			out[i].Acs = *pb_AccessMode_deserialize(acs)
		}
		if subs[i].GetLastSeenTime() > 0 {
			out[i].LastSeen = &MsgLastSeenInfo{
				When:      int64ToTime(subs[i].GetLastSeenTime()),
				UserAgent: subs[i].GetLastSeenUserAgent(),
			}
		}
	}
	return out
}

func pb_DelQuery_serialize(in []MsgDelRange) []*pbx.SeqRange {
	if in == nil {
		return nil
	}

	out := make([]*pbx.SeqRange, len(in))
	for i, dq := range in {
		out[i] = &pbx.SeqRange{}
		if dq.SeqId > 0 {
			out[i].DelId = &pbx.DelQuery_SeqId{SeqId: int32(dq.SeqId)}
		} else {
			out[i].DelId = &pbx.DelQuery_Range{
				Range: &pbx.SeqRange{Low: int32(dq.LowId), Hi: int32(dq.HiId)}}
		}
	}

	return out
}

func pb_DelQuery_deserialize(in []*pbx.SeqRange) []MsgDelRange {
	if in == nil {
		return nil
	}

	out := make([]MsgDelRange, len(in))
	for i, dq := range in {
		if r := dq.GetRange(); r != nil {
			out[i].LowId = int(r.GetLow())
			out[i].HiId = int(r.GetHi())
		} else {
			out[i].SeqId = int(dq.GetSeqId())
		}
	}

	return out
}

func pb_DelValues_serialize(in *MsgDelValues) *pbx.DelValues {
	if in == nil {
		return nil
	}

	return &pbx.DelValues{
		DelId:  int32(in.DelId),
		DelSeq: pb_DelQuery_serialize(in.DelSeq),
	}
}

func pb_DelValues_deserialize(in *pbx.DelValues) *MsgDelValues {
	if in == nil {
		return nil
	}

	return &MsgDelValues{
		DelId:  int(in.GetDelId()),
		DelSeq: pb_DelQuery_deserialize(in.GetDelSeq()),
	}
}
