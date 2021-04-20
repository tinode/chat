package main

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

type Responses struct {
	messages []interface{}
}

type TopicTestHelper struct {
	numUsers int
	uids     []types.Uid

	// Gomock controller.
	ctrl *gomock.Controller

	// Sessions.
	sessions []*Session
	sessWg   *sync.WaitGroup
	// Per-session responses (i.e. what gets dumped into sessions' write loops).
	results []*Responses

	// Hub.
	hub *Hub
	// Messages captured from Hub.route channel on the per-user (RcptTo) basis.
	hubMessages map[string][]*ServerComMessage
	// For stopping hub loop.
	hubDone chan bool

	// Topic.
	topic *Topic
}

func (b *TopicTestHelper) finish() {
	b.topic.killTimer.Stop()
	// Stop session write loops.
	for _, s := range b.sessions {
		close(s.send)
	}
	b.sessWg.Wait()
	// Hub loop.
	close(b.hub.route)
	<-b.hubDone
}

func (b *TopicTestHelper) newSession(sid string, uid types.Uid) (*Session, *Responses) {
	s := &Session{
		sid:  sid,
		uid:  uid,
		subs: make(map[string]*Subscription),
		send: make(chan interface{}, 1)}
	r := &Responses{}
	b.sessWg.Add(1)
	go s.testWriteLoop(r, b.sessWg)
	return s, r
}

func (b *TopicTestHelper) setUp(t *testing.T, numUsers int, cat types.TopicCat, topicName string, attachSessions bool) {
	b.numUsers = numUsers
	b.uids = make([]types.Uid, numUsers)
	for i := 0; i < numUsers; i++ {
		// Can't use 0 as a valid uid.
		b.uids[i] = types.Uid(i + 1)
	}

	b.ctrl = gomock.NewController(t)
	// Sessions.
	b.sessions = make([]*Session, b.numUsers)
	b.results = make([]*Responses, b.numUsers)
	b.sessWg = &sync.WaitGroup{}
	for i := range b.sessions {
		s, r := b.newSession(fmt.Sprintf("sid%d", i), b.uids[i])
		b.results[i] = r
		b.sessions[i] = s
	}

	// Hub.
	b.hub = &Hub{
		route: make(chan *ServerComMessage, 10),
	}
	globals.hub = b.hub
	b.hubMessages = make(map[string][]*ServerComMessage)
	b.hubDone = make(chan bool)
	go b.hub.testHubLoop(t, b.hubMessages, b.hubDone)

	// Topic.
	pu := make(map[types.Uid]perUserData)
	ps := make(map[*Session]perSessionData)
	for i, uid := range b.uids {
		puData := perUserData{
			modeWant:  types.ModeCFull,
			modeGiven: types.ModeCFull,
		}
		if cat == types.TopicCatP2P {
			puData.topicName = b.uids[i^1].UserId()
		}
		pu[uid] = puData
		if attachSessions {
			ps[b.sessions[i]] = perSessionData{uid: uid}
		}
	}
	b.topic = &Topic{
		name:      topicName,
		cat:       cat,
		status:    topicStatusLoaded,
		perUser:   pu,
		isProxy:   false,
		sessions:  ps,
		killTimer: time.NewTimer(time.Hour),
	}
	if cat == types.TopicCatGrp {
		b.topic.xoriginal = topicName
	}
}

func (b *TopicTestHelper) tearDown() {
	globals.hub = nil
	b.ctrl.Finish()
}

func (s *Session) testWriteLoop(results *Responses, wg *sync.WaitGroup) {
	for msg := range s.send {
		results.messages = append(results.messages, msg)
	}
	wg.Done()
}

func (h *Hub) testHubLoop(t *testing.T, results map[string][]*ServerComMessage, done chan bool) {
	for msg := range h.route {
		if msg.RcptTo == "" {
			t.Fatal("Hub.route received a message without addressee.")
			break
		}
		results[msg.RcptTo] = append(results[msg.RcptTo], msg)
	}
	done <- true
}

func TestHandleBroadcastDataP2P(t *testing.T) {
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, "p2p-test", true)
	m := mock_store.NewMockMessagesObjMapperInterface(helper.ctrl)
	store.Messages = m
	defer func() {
		store.Messages = nil
		helper.tearDown()
	}()
	m.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	from := helper.uids[0].UserId()
	msg := &ServerComMessage{
		AsUser: from,
		Data: &MsgServerData{
			Topic:   "p2p",
			From:    from,
			Content: "test",
		},
		sess:    helper.sessions[0],
		SkipSid: helper.sessions[0].sid,
	}
	helper.topic.handleBroadcast(msg)
	helper.finish()

	// Message uid1 -> uid2.
	for i, m := range helper.results {
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
	// Checking presence messages routed through huhelper.
	if len(helper.hubMessages) != 2 {
		t.Fatal("Huhelper.route expected exactly two recepients routed via huhelper.")
	}
	for i, uid := range helper.uids {
		if mm, ok := helper.hubMessages[uid.UserId()]; ok {
			if len(mm) == 1 {
				s := mm[0]
				if s.Pres != nil {
					p := s.Pres
					if p.Topic != "me" {
						t.Errorf("Uid %s: pres notify on topic is expected to be 'me', got %s", uid.UserId(), p.Topic)
					}
					if p.SkipTopic != "p2p-test" {
						t.Errorf("Uid %s: pres skip topic is expected to be 'p2p-test', got %s", uid.UserId(), p.SkipTopic)
					}
					expectedSrc := helper.uids[i^1].UserId()
					if p.Src != expectedSrc {
						t.Errorf("Uid %s: pres.src expected: %s, found: %s", uid.UserId(), expectedSrc, p.Src)
					}
				} else {
					t.Errorf("Uid %s: hub message expected to be {pres}.", uid.UserId())
				}
			} else {
				t.Errorf("Uid %s: expected 1 hub message, got %d.", uid.UserId(), len(mm))
			}
		} else {
			t.Errorf("Uid %s: no hub results found.", uid.UserId())
		}
	}
}

func TestHandleBroadcastDataGroup(t *testing.T) {
	topicName := "grp-test"
	numUsers := 4
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	m := mock_store.NewMockMessagesObjMapperInterface(helper.ctrl)
	store.Messages = m
	defer func() {
		store.Messages = nil
		helper.tearDown()
	}()
	m.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	// User 3 isn't allowed to read.
	pu3 := helper.topic.perUser[helper.uids[3]]
	pu3.modeWant = types.ModeJoin | types.ModeWrite | types.ModePres
	pu3.modeGiven = pu3.modeWant
	helper.topic.perUser[helper.uids[3]] = pu3

	from := helper.uids[0].UserId()
	msg := &ServerComMessage{
		AsUser: from,
		Data: &MsgServerData{
			Topic:   "group",
			From:    from,
			Content: "test",
		},
		sess:    helper.sessions[0],
		SkipSid: helper.sessions[0].sid,
	}
	helper.topic.handleBroadcast(msg)
	helper.finish()

	// Message uid0 -> uid1, uid2, uid3.
	// Uid0 is the sender.
	if len(helper.results[0].messages) != 0 {
		t.Fatalf("Uid0 is the sender: expected 0 messages, got %d", len(helper.results[0].messages))
	}
	// Uid3 is not a topic reader.
	if len(helper.results[3].messages) != 0 {
		t.Fatalf("Uid3 isn't allowed to read messages: expected 0 messages, got %d", len(helper.results[3].messages))
	}
	for i := 1; i < 3; i++ {
		m := helper.results[i]
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
	// Presence messages.
	if len(helper.hubMessages) != 3 {
		t.Fatal("Huhelper.route expected exactly three recepients routed via huhelper.")
	}
	for i, uid := range helper.uids {
		if i == 3 {
			if _, ok := helper.hubMessages[uid.UserId()]; ok {
				t.Errorf("Uid %s: not expected to receive pres notifications.", uid.UserId())
			}
			continue
		}
		if mm, ok := helper.hubMessages[uid.UserId()]; ok {
			if len(mm) == 1 {
				s := mm[0]
				if s.Pres != nil {
					p := s.Pres
					if p.Topic != "me" {
						t.Errorf("Uid %s: pres notify on topic is expected to be 'me', got %s", uid.UserId(), p.Topic)
					}
					if p.SkipTopic != topicName {
						t.Errorf("Uid %s: pres skip topic is expected to be 'p2p-test', got %s", uid.UserId(), p.SkipTopic)
					}
					if p.Src != topicName {
						t.Errorf("Uid %s: pres.src expected: %s, found: %s", uid.UserId(), topicName, p.Src)
					}
				} else {
					t.Errorf("Uid %s: hub message expected to be {pres}.", uid.UserId())
				}
			} else {
				t.Errorf("Uid %s: expected 1 hub message, got %d.", uid.UserId(), len(mm))
			}
		} else {
			t.Errorf("Uid %s: no hub results found.", uid.UserId())
		}
	}
}

func TestHandleBroadcastDataInactiveTopic(t *testing.T) {
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, "p2p-test", true)
	defer helper.tearDown()

	// Make test message.
	from := helper.uids[0].UserId()
	msg := &ServerComMessage{
		AsUser: from,
		Data: &MsgServerData{
			Topic:   "p2p",
			From:    from,
			Content: "test",
		},
		sess:    helper.sessions[0],
		SkipSid: helper.sessions[0].sid,
	}

	// Deactivate topic.
	helper.topic.markDeleted()

	helper.topic.handleBroadcast(msg)
	helper.finish()

	// Message uid1 -> uid2.
	if len(helper.results[0].messages) == 1 {
		em := helper.results[0].messages[0].(*ServerComMessage)
		if em.Ctrl == nil {
			t.Fatal("User 1 is expected to receive a ctrl message")
		}
		if em.Ctrl.Code < 500 || em.Ctrl.Code >= 600 {
			t.Errorf("User1: expected ctrl.code 5xx, received %d", em.Ctrl.Code)
		}
	} else {
		t.Errorf("User 1 is expected to receive one message vs %d received.", len(helper.results[0].messages))
	}
	if len(helper.results[1].messages) != 0 {
		t.Errorf("User 2 is not expected to receive any messages, %d received.", len(helper.results[1].messages))
	}
	// Checking presence messages routed through huhelper.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Huhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestReplyGetDescInvalidOpts(t *testing.T) {
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, "", true)
	defer helper.tearDown()

	msg := ClientComMessage{
		Original: "dummy",
	}
	// Can't specify User in opts.
	if err := helper.topic.replyGetDesc(helper.sessions[0], 123, false, &MsgGetOpts{User: "abcdef"}, &msg); err == nil {
		t.Error("replyGetDesc expected to error out.")
	} else if err.Error() != "invalid GetDesc query" {
		t.Errorf("Unexpected error: expected 'invalid GetDesc query', got '%s'", err.Error())
	}
	helper.finish()

	if len(helper.results[0].messages) != 1 {
		t.Fatalf("`responses` expected to contain 1 element, found %d", len(helper.results[0].messages))
	}
	resp := helper.results[0].messages[0].(*ServerComMessage)
	if resp.Ctrl == nil {
		t.Fatalf("response expected to contain a Ctrl message")
	}
	if resp.Ctrl.Code != 400 {
		t.Errorf("response code: expected 400, found: %d", resp.Ctrl.Code)
	}
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionMe(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, false)
	uu := mock_store.NewMockUsersObjMapperInterface(helper.ctrl)
	tt := mock_store.NewMockTopicsObjMapperInterface(helper.ctrl)
	ss := mock_store.NewMockSubsObjMapperInterface(helper.ctrl)
	store.Users = uu
	store.Topics = tt
	store.Subs = ss
	defer func() {
		store.Users = nil
		store.Topics = nil
		store.Subs = nil
		helper.tearDown()
	}()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	uid := helper.uids[0]

	// Add a couple more sessions.
	for i := 1; i < 3; i++ {
		s, r := helper.newSession(fmt.Sprintf("sid%d", i), uid)
		helper.sessions = append(helper.sessions, s)
		helper.results = append(helper.results, r)
	}

	for i, s := range helper.sessions {
		join := &sessionJoin{
			pkt: &ClientComMessage{
				Sub: &MsgClientSub{
					Id:    fmt.Sprintf("id456-%d", i),
					Topic: "me",
				},
				AsUser: uid.UserId(),
			},
			sess: s,
		}
		helper.topic.registerSession(join)
	}
	helper.finish()

	if len(helper.topic.sessions) != 3 {
		t.Errorf("Attached sessions: expected 1, found %d", len(helper.topic.sessions))
	}
	for _, s := range helper.sessions {
		if len(s.subs) != 1 {
			t.Errorf("Session subscriptions: expected 1, found %d", len(s.subs))
		}
	}
	online := helper.topic.perUser[uid].online
	if online != 3 {
		t.Errorf("Number of online sessions: expected 3, found %d", online)
	}
	// Session output.
	for _, r := range helper.results {
		if len(r.messages) != 1 {
			t.Fatalf("`responses` expected to contain 1 element, found %d", len(helper.results[0].messages))
		}
		resp := r.messages[0].(*ServerComMessage)
		if resp.Ctrl == nil {
			t.Fatalf("response expected to contain a Ctrl message")
		}
		if resp.Ctrl.Code != 200 {
			t.Errorf("response code: expected 200, found: %d", resp.Ctrl.Code)
		}
	}
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestMain(m *testing.M) {
	logs.Init(os.Stderr, "stdFlags")
	os.Exit(m.Run())
}
