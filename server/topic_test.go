package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

type Responses struct {
	messages []interface{}
}

func (s *Session) testWriteLoop(results *Responses, wg *sync.WaitGroup) {
	for msg := range s.send {
		results.messages = append(results.messages, msg)
	}
	wg.Done()
}

func (h *Hub) testHubLoop(done chan bool) {
	for range h.route {
		// TODO: check routed pres messages.
	}
	done <- true
}

func TestHandleBroadcastP2P(t *testing.T) {
	uid1 := types.Uid(1)
	uid2 := types.Uid(2)

	ctrl := gomock.NewController(t)
	m := mock_store.NewMockMessagesObjMapperInterface(ctrl)
	store.Messages = m
	defer func() {
		store.Messages = nil
		ctrl.Finish()
	}()
	m.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	ss := make([]*Session, 2)
	messages := make([]*Responses, 2)
	sessWg := sync.WaitGroup{}
	for i := range ss {
		ss[i] = &Session{sid: fmt.Sprintf("sid%d", i)}
		ss[i].send = make(chan interface{}, 1)
		messages[i] = &Responses{}
		sessWg.Add(1)
		go ss[i].testWriteLoop(messages[i], &sessWg)
	}

	h := &Hub{
		route: make(chan *ServerComMessage, 10),
	}
	globals.hub = h
	hubDone := make(chan bool)
	go h.testHubLoop(hubDone)

	topic := Topic{
		name:   "p2p-test",
		cat:    types.TopicCatP2P,
		status: topicStatusLoaded,
		perUser: map[types.Uid]perUserData{
			uid1: perUserData{
				modeWant:  types.ModeCP2P,
				modeGiven: types.ModeCP2P,
			},
			uid2: perUserData{
				modeWant:  types.ModeCP2P,
				modeGiven: types.ModeCP2P,
			},
		},
		isProxy: false,
		sessions: map[*Session]perSessionData{
			ss[0]: perSessionData{uid: uid1},
			ss[1]: perSessionData{uid: uid2},
		},
	}
	msg := &ServerComMessage{
		AsUser: uid1.UserId(),
		Data: &MsgServerData{
			Topic:   "p2p",
			From:    uid1.UserId(),
			Content: "test",
		},
		sess:    ss[0],
		SkipSid: ss[0].sid,
	}
	topic.handleBroadcast(msg)
	// Stop session write loops.
	for _, s := range ss {
		close(s.send)
	}
	sessWg.Wait()
	// Hub loop.
	close(h.route)
	<-hubDone
	// Message uid1 -> uid2.
	for i, m := range messages {
		if i == 0 {
			if len(m.messages) != 0 {
				t.Fatalf("Uid1: expected 0 messages, got %d", len(m.messages))
			}
		} else {
			if len(m.messages) != 1 {
				t.Fatalf("Uid2: expected 1 messages, got %d", len(m.messages))
			}
			r := m.messages[0].(*ServerComMessage)
			if r.Data == nil {
				t.Fatalf("Response[0] must have a ctrl message")
			}
			if r.Data.Content.(string) != "test" {
				t.Errorf("Response[0] content: expected 'test', got '%s'", r.Data.Content.(string))
			}
		}
	}
}

func TestHandleBroadcastGroup(t *testing.T) {
	numUsers := 4
	uids := make([]types.Uid, numUsers)
	for i := 0; i < numUsers; i++ {
		uids[i] = types.Uid(i)
	}

	ctrl := gomock.NewController(t)
	m := mock_store.NewMockMessagesObjMapperInterface(ctrl)
	store.Messages = m
	defer func() {
		store.Messages = nil
		ctrl.Finish()
	}()
	m.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	ss := make([]*Session, numUsers)
	results := make([]*Responses, numUsers)
	sessWg := sync.WaitGroup{}
	for i := range ss {
		ss[i] = &Session{sid: fmt.Sprintf("sid%d", i)}
		ss[i].send = make(chan interface{}, 1)
		results[i] = &Responses{}
		sessWg.Add(1)
		go ss[i].testWriteLoop(results[i], &sessWg)
	}

	h := &Hub{
		route: make(chan *ServerComMessage, 10),
	}
	globals.hub = h
	hubDone := make(chan bool)
	go h.testHubLoop(hubDone)

	// User 3 isn't allowed to read.
	perms := []types.AccessMode{types.ModeCFull, types.ModeCFull, types.ModeCFull, types.ModeJoin | types.ModeWrite}
	pu := make(map[types.Uid]perUserData)
	ps := make(map[*Session]perSessionData)
	for i, uid := range uids {
		pu[uid] = perUserData{
			modeWant:  perms[i],
			modeGiven: perms[i],
		}
		ps[ss[i]] = perSessionData{uid: uid}
	}
	topic := Topic{
		name:     "group-test",
		cat:      types.TopicCatGrp,
		status:   topicStatusLoaded,
		perUser:  pu,
		isProxy:  false,
		sessions: ps,
	}
	from := uids[0].UserId()
	msg := &ServerComMessage{
		AsUser: from,
		Data: &MsgServerData{
			Topic:   "group",
			From:    from,
			Content: "test",
		},
		sess:    ss[0],
		SkipSid: ss[0].sid,
	}
	topic.handleBroadcast(msg)
	// Stop session write loops.
	for _, s := range ss {
		close(s.send)
	}
	sessWg.Wait()
	// Hub loop.
	close(h.route)
	<-hubDone
	// Message uid0 -> uid1, uid2, uid3.
	// Uid0 is the sender.
	if len(results[0].messages) != 0 {
		t.Fatalf("Uid0 is the sender: expected 0 messages, got %d", len(results[0].messages))
	}
	// Uid3 is not a topic reader.
	if len(results[3].messages) != 0 {
		t.Fatalf("Uid3 isn't allowed to read messages: expected 0 messages, got %d", len(results[3].messages))
	}
	for i := 1; i < 3; i++ {
		m := results[i]
		if len(m.messages) != 1 {
			t.Fatalf("Uid%d: expected 1 messages, got %d", i, len(m.messages))
		}
		r := m.messages[0].(*ServerComMessage)
		if r.Data == nil {
			t.Fatalf("Response[0] must have a ctrl message")
		}
		if r.Data.Content.(string) != "test" {
			t.Errorf("Response[0] content: expected 'test', got '%s'", r.Data.Content.(string))
		}
	}
}

func TestReplyGetDescInvalidOpts(t *testing.T) {
	var sess Session
	sess.send = make(chan interface{}, 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	var responses Responses
	go sess.testWriteLoop(&responses, &wg)

	topic := Topic{
		cat: types.TopicCatMe,
	}

	msg := ClientComMessage{
		Original: "dummy",
	}
	// Can't specify User in opts.
	if err := topic.replyGetDesc(&sess, 123, false, &MsgGetOpts{User: "abcdef"}, &msg); err == nil {
		t.Error("replyGetDesc expected to error out.")
	} else if err.Error() != "invalid GetDesc query" {
		t.Errorf("Unexpected error: expected 'invalid GetDesc query', got '%s'", err.Error())
	}
	close(sess.send)
	// Wait for the session's write loop to complete.
	wg.Wait()
	if len(responses.messages) != 1 {
		t.Fatalf("`responses` expected to contain 1 element, found %d", len(responses.messages))
	}
	resp := responses.messages[0].(*ServerComMessage)
	if resp.Ctrl == nil {
		t.Fatalf("response expected to contain a Ctrl message")
	}
	if resp.Ctrl.Code != 400 {
		t.Errorf("response code: expected 400, found: %d", resp.Ctrl.Code)
	}
}
