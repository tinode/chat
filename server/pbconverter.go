// Converts between protobuf structs and Go representation of packets

package main

import (
	"encoding/json"
	"time"

	"github.com/tinode/chat/pbx"
)

// Convert ServerComMessage to pbx.ServerMsg
func pb_serv_serialize(msg *ServerComMessage) *pbx.ServerMsg {
	return nil
}

// Convert ClientComMessage to pbx.ClientMsg
func pb_cli_serialize(msg *ClientComMessage) *pbx.ClientMsg {
	var pkt pbx.ClientMsg

	if msg.Hi != nil {
		pkt.Message = &pbx.ClientMsg_Hi{Hi: &pbx.ClientHi{
			Id:        msg.Hi.Id,
			UserAgent: msg.Hi.UserAgent,
			Ver:       int32(parseVersion(msg.Hi.Version)),
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
		content, _ := json.Marshal(msg.Pub.Content)
		pkt.Message = &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{
			Id:      msg.Pub.Id,
			Topic:   msg.Pub.Topic,
			NoEcho:  msg.Pub.NoEcho,
			Head:    msg.Pub.Head,
			Content: content}}
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
			Id:      msg.Del.Id,
			Topic:   msg.Del.Topic,
			What:    what,
			Before:  int32(msg.Del.Before),
			SeqList: intSliceToInt32(msg.Del.SeqList),
			UserId:  msg.Del.User,
			Hard:    msg.Del.Hard}}
	} else if msg.Note != nil {
		var what pbx.InfoNote
		switch msg.Note.What {
		case "kp":
			what = pbx.InfoNote_KP
		case "read":
			what = pbx.InfoNote_READ
		case "recv":
			what = pbx.InfoNote_RECV
		}
		pkt.Message = &pbx.ClientMsg_Note{Note: &pbx.ClientNote{
			Topic: msg.Note.Topic,
			What:  what,
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
			Version:   versionToString(int(hi.GetVer())),
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
		}
		if desc := acc.GetDesc(); desc != nil {
			msg.Acc.Desc = &MsgSetDesc{
				// FIXME: public & private assignments are broken.
				// they should be assumed to be JSON and unmarshalled.
				Public:  desc.GetPublic(),
				Private: desc.GetPrivate(),
			}
			if acs := desc.GetDefaultAcs(); acs != nil {
				msg.Acc.Desc.DefaultAcs = &MsgDefaultAcsMode{
					Auth: acs.GetAuth(),
					Anon: acs.GetAnon(),
				}
			}
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
		}

		if set := sub.GetSetQuery(); set != nil {
			msg.Sub.Set = &MsgSetQuery{}
			if desc := set.GetDesc(); desc != nil {
				msg.Sub.Set.Desc = &MsgSetDesc{}
			}
			if sub := set.GetSub(); sub != nil {
				msg.Sub.Set.Sub = &MsgSetSub{}
			}
		}
		if get := sub.GetGetQuery(); get != nil {
			msg.Sub.Get = &MsgGetQuery{}
		}
	} else if leave := pkt.GetLeave(); leave != nil {
		msg.Leave = &MsgClientLeave{
			Id:    leave.GetId(),
			Topic: leave.GetTopic(),
			Unsub: leave.GetUnsub(),
		}
	} else if pub := pkt.GetPub(); pub != nil {
		msg.Pub = &MsgClientPub{
			Id:     pub.GetId(),
			Topic:  pub.GetTopic(),
			NoEcho: pub.GetNoEcho(),
			Head:   pub.GetHead(),
			// FIXME: assume JSON, convert to map.
			Content: pub.GetContent(),
		}
	} else if get := pkt.GetGet(); get != nil {
		msg.Get = &MsgClientGet{
			Id:    get.GetId(),
			Topic: get.GetTopic(),
		}
		if gq := get.GetQuery(); gq != nil {
			msg.Get.MsgGetQuery = MsgGetQuery{
				What: gq.GetWhat(),
				Desc: gq.GetDesc(),
				Sub:  gq.GetSub(),
				Data: gq.GetData(),
			}
		}
	} else if set := pkt.GetSet(); set != nil {
		msg.Set = &MsgClientSet{
			Id: set.GetId(),
		}
	} else if del := pkt.GetDel(); del != nil {
		msg.Del = &MsgClientDel{
			Id:      del.GetId(),
			Topic:   del.GetTopic(),
			Before:  int(del.GetBefore()),
			SeqList: int32SliceToInt(del.GetSeqList()),
			User:    del.GetUserId(),
			Hard:    del.GetHard(),
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

func in64ToTime(ts int64) *time.Time {
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

	convertTimeStamp := func(IfModifiedSince *time.Time) int64 {
		var ims int64
		if IfModifiedSince != nil {
			ims = IfModifiedSince.UnixNano() / 1000000
		}
		return ims
	}

	if in.Desc != nil {
		out.Desc = &pbx.GetOpts{
			IfModifiedSince: convertTimeStamp(in.Desc.IfModifiedSince),
			Limit:           int32(in.Desc.Limit)}
	}
	if in.Sub != nil {
		out.Sub = &pbx.GetOpts{
			IfModifiedSince: convertTimeStamp(in.Sub.IfModifiedSince),
			Limit:           int32(in.Sub.Limit)}
	}
	if in.Data != nil {
		out.Data = &pbx.BrowseOpts{
			BeforeId: int32(in.Data.BeforeId),
			BeforeTs: convertTimeStamp(in.Data.BeforeTs),
			SinceId:  int32(in.Data.SinceId),
			SinceTs:  convertTimeStamp(in.Data.SinceTs),
			Limit:    int32(in.Data.Limit)}
	}
	return out
}

func pb_SetDesc_serialize(in *MsgSetDesc) *pbx.SetDesc {
	if in == nil {
		return nil
	}

	out := &pbx.SetDesc{}
	if in.DefaultAcs != nil {
		out.DefaultAcs = &pbx.DefaultAcsMode{
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
