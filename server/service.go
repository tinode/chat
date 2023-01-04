package main

import (
	"github.com/Limuwenan/chat/server/logs"
	"github.com/Limuwenan/chat/server/store"
	"github.com/Limuwenan/chat/server/store/types"
	"time"
)

func selectService(s *Session, msg *ClientComMessage) {
	adapter := store.Store.GetAdapter()
	opts := msg.Ser.Get
	msgs, err := adapter.MessageServiceGetAll(opts.Topic, types.ParseUserId(opts.User), opts.SinceId, opts.BeforeId, opts.Limit)
	if err != nil {
		logs.Warn.Println()
	}

	// 构造并发送数据
	outgoingMessages := make([]*ServerComMessage, len(msgs))
	for i := range msgs {
		logs.Info.Println(msgs[i])
		outgoingMessages[i] = &ServerComMessage{
			Data: &MsgServerData{
				Topic:     opts.Topic,
				SeqId:     msgs[i].SeqId,
				From:      msgs[i].From,
				Timestamp: msgs[i].CreatedAt,
				Content:   msgs[i].Content,
			},
		}
	}
	s.queueOutBatch(outgoingMessages)
	resp := &ServerComMessage{
		Ctrl: &MsgServerCtrl{
			Topic:     opts.Topic,
			Code:      200,
			Text:      "Get messages successful",
			Timestamp: time.Now(),
		},
	}
	s.queueOut(resp)
}

func updateService(s *Session, msg *ClientComMessage) {
	s.queueOutBytes([]byte("update msg"))
}

func createService(s *Session, msg *ClientComMessage) {
	s.queueOutBytes([]byte("create msg"))
}

func deleteService(s *Session, msg *ClientComMessage) {
	s.queueOutBytes([]byte("delete msg"))
}
