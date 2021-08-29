package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

type Responses struct {
	messages []interface{}
}

// Test fixture.
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

	// Mock objects.
	mm *mock_store.MockMessagesPersistenceInterface
	uu *mock_store.MockUsersPersistenceInterface
	tt *mock_store.MockTopicsPersistenceInterface
	ss *mock_store.MockSubsPersistenceInterface
}

func (b *TopicTestHelper) finish() {
	b.topic.killTimer.Stop()
	// Stop session write loops.
	for _, s := range b.sessions {
		close(s.send)
	}
	b.sessWg.Wait()
	// Hub loop.
	close(b.hub.routeSrv)
	close(b.hub.routeCli)
	<-b.hubDone
}

func (b *TopicTestHelper) newSession(sid string, uid types.Uid) (*Session, *Responses) {
	s := &Session{
		sid:    sid,
		uid:    uid,
		subs:   make(map[string]*Subscription),
		send:   make(chan interface{}, 10),
		detach: make(chan string, 10),
	}
	r := &Responses{}
	b.sessWg.Add(1)
	go s.testWriteLoop(r, b.sessWg)
	return s, r
}

func (b *TopicTestHelper) setUp(t *testing.T, numUsers int, cat types.TopicCat, topicName string, attachSessions bool) {
	t.Helper()
	b.numUsers = numUsers
	b.uids = make([]types.Uid, numUsers)
	for i := 0; i < numUsers; i++ {
		// Can't use 0 as a valid uid.
		b.uids[i] = types.Uid(i + 1)
	}

	// Mocks.
	b.ctrl = gomock.NewController(t)
	b.mm = mock_store.NewMockMessagesPersistenceInterface(b.ctrl)
	b.uu = mock_store.NewMockUsersPersistenceInterface(b.ctrl)
	b.tt = mock_store.NewMockTopicsPersistenceInterface(b.ctrl)
	b.ss = mock_store.NewMockSubsPersistenceInterface(b.ctrl)
	store.Messages = b.mm
	store.Users = b.uu
	store.Topics = b.tt
	store.Subs = b.ss
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
		routeCli: make(chan *ClientComMessage, 10),
		routeSrv: make(chan *ServerComMessage, 10),
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
		if attachSessions {
			ps[b.sessions[i]] = perSessionData{uid: uid}
			puData.online = 1
		}
		pu[uid] = puData
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
	if cat != types.TopicCatSys {
		b.topic.accessAuth = getDefaultAccess(cat, true, false)
		b.topic.accessAnon = getDefaultAccess(cat, true, false)
	}
	if cat == types.TopicCatMe {
		b.topic.xoriginal = "me"
	}
	if cat == types.TopicCatGrp {
		b.topic.xoriginal = topicName
		b.topic.owner = b.uids[0]
	}
}

func (b *TopicTestHelper) tearDown() {
	globals.hub = nil
	store.Messages = nil
	store.Users = nil
	store.Topics = nil
	store.Subs = nil
	b.ctrl.Finish()
}

func (s *Session) testWriteLoop(results *Responses, wg *sync.WaitGroup) {
	for msg := range s.send {
		results.messages = append(results.messages, msg)
	}
	wg.Done()
}

func (h *Hub) testHubLoop(t *testing.T, results map[string][]*ServerComMessage, done chan bool) {
	t.Helper()
	for msg := range h.routeSrv {
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
	helper.setUp(t, numUsers, types.TopicCatP2P, "p2p-test" /*attach=*/, true)
	defer helper.tearDown()
	helper.mm.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	from := helper.uids[0].UserId()
	msg := &ClientComMessage{
		AsUser:   from,
		Original: from,
		Pub: &MsgClientPub{
			Topic:   "p2p",
			Content: "test",
			NoEcho:  true,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
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
			if r.Data.Topic != from {
				t.Errorf("Response[0] topic: expected '%s', got '%s'", from, r.Data.Topic)
			}
			if r.Data.Content.(string) != "test" {
				t.Errorf("Response[0] content: expected 'test', got '%s'", r.Data.Content.(string))
			}
			if r.Data.From != from {
				t.Errorf("Response[0] from: expected '%s', got '%s'", from, r.Data.From)
			}
		}
	}
	// Checking presence messages routed through the helper.
	if len(helper.hubMessages) != 2 {
		t.Fatal("Huhelper.route expected exactly two recipients routed via huhelper.")
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
	defer func() {
		store.Messages = nil
		helper.tearDown()
	}()
	helper.mm.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	// User 3 isn't allowed to read.
	pu3 := helper.topic.perUser[helper.uids[3]]
	pu3.modeWant = types.ModeJoin | types.ModeWrite | types.ModePres
	pu3.modeGiven = pu3.modeWant
	helper.topic.perUser[helper.uids[3]] = pu3

	from := helper.uids[0].UserId()
	msg := &ClientComMessage{
		AsUser:   from,
		Original: topicName,
		Pub: &MsgClientPub{
			Topic:   topicName,
			Content: "test",
			NoEcho:  true,
		},
		sess: helper.sessions[0],
	}

	if helper.topic.lastID != 0 {
		t.Errorf("Topic.lastID: expected 0, found %d", helper.topic.lastID)
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	if helper.topic.lastID != 1 {
		t.Errorf("Topic.lastID: expected 1, found %d", helper.topic.lastID)
	}
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
		if r.Data.Topic != topicName {
			t.Errorf("Response[0] topic: expected '%s', got '%s'", topicName, r.Data.Topic)
		}
		if r.Data.From != from {
			t.Errorf("Response[0] from: expected '%s', got '%s'", from, r.Data.From)
		}
		if r.Data.Content.(string) != "test" {
			t.Errorf("Response[0] content: expected 'test', got '%s'", r.Data.Content.(string))
		}
	}
	// Presence messages.
	if len(helper.hubMessages) != 3 {
		t.Fatal("Hubhelper.route expected exactly three recipients routed via huhelper.")
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

func TestHandleBroadcastDataMissingWritePermission(t *testing.T) {
	topicName := "p2p-test"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()

	// Remove W permission for uid1.
	uid1 := helper.uids[0]
	pud := helper.topic.perUser[uid1]
	pud.modeGiven = types.ModeRead | types.ModeJoin
	helper.topic.perUser[uid1] = pud

	// Make test message.
	from := helper.uids[0].UserId()
	msg := &ClientComMessage{
		AsUser:   from,
		Original: from,
		Pub: &MsgClientPub{
			Topic:   "p2p",
			Content: "test",
		},
		sess: helper.sessions[0],
	}

	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Message uid1 -> uid2.
	if len(helper.results[0].messages) == 1 {
		em := helper.results[0].messages[0].(*ServerComMessage)
		if em.Ctrl == nil {
			t.Fatal("User 1 is expected to receive a ctrl message")
		}
		if em.Ctrl.Code < 400 || em.Ctrl.Code >= 500 {
			t.Errorf("User1: expected ctrl.code 4xx, received %d", em.Ctrl.Code)
		}
	} else {
		t.Errorf("User 1 is expected to receive one message vs %d received.", len(helper.results[0].messages))
	}
	if len(helper.results[1].messages) != 0 {
		t.Errorf("User 2 is not expected to receive any messages, %d received.", len(helper.results[1].messages))
	}
	// Checking presence messages routed through hubhelper.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastDataDbError(t *testing.T) {
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, "p2p-test", true)
	defer helper.tearDown()

	// DB returns an error.
	helper.mm.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(types.ErrInternal)

	// Make test message.
	from := helper.uids[0].UserId()
	msg := &ClientComMessage{
		AsUser: from,
		Pub: &MsgClientPub{
			Topic:   "p2p",
			Content: "test",
		},
		sess: helper.sessions[0],
	}

	if helper.topic.lastID != 0 {
		t.Errorf("Topic.lastID: expected 0, found %d", helper.topic.lastID)
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	if helper.topic.lastID != 0 {
		t.Errorf("Topic.lastID: expected to remain 0, found %d", helper.topic.lastID)
	}
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
	// Checking presence messages routed through hubhelper.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastDataInactiveTopic(t *testing.T) {
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, "p2p-test", true)
	defer helper.tearDown()

	// Make test message.
	from := helper.uids[0].UserId()
	msg := &ClientComMessage{
		AsUser: from,
		Pub: &MsgClientPub{
			Topic:   "p2p",
			Content: "test",
		},
		sess: helper.sessions[0],
	}

	// Deactivate topic.
	helper.topic.markDeleted()

	helper.topic.handleClientMsg(msg)
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
	// Checking presence messages routed through hubhelper.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoP2P(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	readId := 8
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 8.
	from := helper.uids[0]
	to := helper.uids[1]

	helper.ss.EXPECT().Update(topicName, from, map[string]interface{}{"ReadSeqId": readId}).Return(nil)

	msg := &ClientComMessage{
		AsUser: from.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Topic metadata.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != readId {
		t.Errorf("perUser[%s].readID: expected %d, found %d.", from.UserId(), readId, actualReadId)
	}
	// Server messages.
	if len(helper.results[0].messages) != 0 {
		t.Errorf("Session 0 isn't expected to receive any messages. Received %d", len(helper.results[0].messages))
	}
	if len(helper.results[1].messages) != 1 {
		t.Fatalf("Session 1 is expected to receive exactly 1 message. Received %d", len(helper.results[1].messages))
	}
	res := helper.results[1].messages[0].(*ServerComMessage)
	if res.Info != nil {
		info := res.Info
		// Topic name will be fixed (to -> from).
		if info.Topic != from.UserId() {
			t.Errorf("Info.Topic: expected '%s', found '%s'", to.UserId(), info.Topic)
		}
		if info.From != from.UserId() {
			t.Errorf("Info.From: expected '%s', found '%s'", from.UserId(), info.From)
		}
		if info.What != "read" {
			t.Errorf("Info.What: expected 'read', found '%s'", info.What)
		}
		if info.SeqId != readId {
			t.Errorf("Info.SeqId: expected %d, found %d", readId, info.SeqId)
		}
	} else {
		t.Error("Session message is expected to contain `info` section.")
	}
	// Checking presence messages routed through hub helper. These are intended for offline sessions.
	if len(helper.hubMessages) != 2 {
		t.Fatalf("Hubhelper.route expected exactly two recipients routed via hubhelper. Found %d", len(helper.hubMessages))
	}
	for i, uid := range helper.uids {
		if routedMsgs, ok := helper.hubMessages[uid.UserId()]; ok {
			expectedSrc := helper.uids[i^1].UserId()
			for _, s := range routedMsgs {
				if s.Info != nil {
					// Info messages for offline sessions.
					info := s.Info
					if info.Topic != "me" {
						t.Errorf("Uid %s: info.topic is expected to be 'me', got %s", uid.UserId(), info.Topic)
					}
					if info.Src != expectedSrc {
						t.Errorf("Uid %s: info.src expected: %s, found: %s", uid.UserId(), expectedSrc, info.Src)
					}
					if info.What != "read" {
						t.Error("info.what expected to be 'read'")
					}
					if info.SeqId != readId {
						t.Errorf("info.seq: expected %d, found %d", readId, info.SeqId)
					}
				} else if s.Pres != nil {
					// Pres messages for offline sessions.
					pres := s.Pres
					if pres.Topic != "me" {
						t.Errorf("Uid %s: pres.topic is expected to be 'me', got %s", uid.UserId(), pres.Topic)
					}
					if pres.What != "read" {
						t.Error("pres.what expected to be 'read'")
					}
					if pres.Src != expectedSrc {
						t.Errorf("Uid %s: pres.src expected: %s, found: %s", uid.UserId(), expectedSrc, pres.Src)
					}
					if pres.SeqId != readId {
						t.Errorf("pres.seq: expected %d, found %d", readId, pres.SeqId)
					}
				} else {
					t.Error("Hub messages must be either `info` or `pres`.")
				}
			}
		} else {
			t.Errorf("Uid %s: no hub results found.", uid.UserId())
		}
	}
}

func TestHandleBroadcastInfoBogusNotification(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 11
	from := helper.uids[0]
	to := helper.uids[1]

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: to.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoFilterOutRecvWithoutRPermission(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	to := helper.uids[1]

	// Revoke R permission from the sender.
	pud := helper.topic.perUser[from]
	pud.modeGiven = types.ModeWrite | types.ModeJoin
	helper.topic.perUser[from] = pud

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: to.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "recv",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoFilterOutKpWithoutWPermission(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	to := helper.uids[1]

	// Revoke W permission from the sender.
	pud := helper.topic.perUser[from]
	pud.modeGiven = types.ModeRead | types.ModeJoin
	helper.topic.perUser[from] = pud

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: to.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "kp",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoDuplicatedRead(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName /*attach=*/, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	to := helper.uids[1]

	// Revoke R permission from the sender.
	pud := helper.topic.perUser[from]
	pud.readID = 8
	helper.topic.perUser[from] = pud

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: to.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 8 {
		t.Errorf("perUser[%s].readID: expected 8, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoDbError(t *testing.T) {
	topicName := "usrP2P"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatP2P, topicName, true)
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	to := helper.uids[1]

	helper.ss.EXPECT().Update(topicName, from, map[string]interface{}{"ReadSeqId": readId}).Return(types.ErrInternal)

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: to.UserId(),
		Note: &MsgClientNote{
			Topic: to.UserId(),
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoInvalidChannelAccess(t *testing.T) {
	topicName := "grpTest"
	chanName := "chnTest"
	numUsers := 3
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	// This is not a channel. However, we will try to handle an info message where
	// the topic is referenced as "chn".
	helper.topic.isChan = false
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	for i := 1; i < numUsers; i++ {
		uid := helper.uids[i]
		pud := helper.topic.perUser[uid]
		pud.modeGiven = types.ModeCChnReader
		helper.topic.perUser[uid] = pud
	}

	msg := &ClientComMessage{
		Original: chanName,
		AsUser:   from.UserId(),
		Note: &MsgClientNote{
			Topic: chanName,
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Read id should not be updated.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}
	// Nothing should be routed through the hub.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
}

func TestHandleBroadcastInfoChannelProcessing(t *testing.T) {
	topicName := "grpTest"
	chanName := "chnTest"
	numUsers := 3
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	helper.topic.isChan = true
	defer helper.tearDown()
	// Pretend we have 10 messages.
	helper.topic.lastID = 10
	// uid1 notifies uid2 that uid1 has read messages up to seqid 11.
	readId := 8
	from := helper.uids[0]
	for i := 1; i < numUsers; i++ {
		uid := helper.uids[i]
		pud := helper.topic.perUser[uid]
		pud.modeGiven = types.ModeCChnReader
		pud.isChan = true
		helper.topic.perUser[uid] = pud
	}

	helper.ss.EXPECT().Update(chanName, from, map[string]interface{}{"ReadSeqId": readId}).Return(nil)

	msg := &ClientComMessage{
		AsUser:   from.UserId(),
		Original: chanName,
		Note: &MsgClientNote{
			Topic: chanName,
			What:  "read",
			SeqId: readId,
		},
		sess: helper.sessions[0],
	}
	helper.topic.handleClientMsg(msg)
	helper.finish()

	// Topic metadata.
	// We do not update read ids for channel topics.
	if actualReadId := helper.topic.perUser[from].readID; actualReadId != 0 {
		t.Errorf("perUser[%s].readID: expected 0, found %d.", from.UserId(), actualReadId)
	}
	// Server messages. Note messages aren't forwarded by channel topics.
	for i, r := range helper.results {
		if numMessages := len(r.messages); numMessages != 0 {
			t.Errorf("User %d is not expected to receive any messages, %d received.", i, numMessages)
		}
	}

	// Send a pres back to the sender.
	if len(helper.hubMessages) != 1 {
		t.Fatalf("Hubhelper.route did not expect any messages, however %d received.", len(helper.hubMessages))
	}
	if mm, ok := helper.hubMessages[from.UserId()]; ok || len(mm) != 1 {
		s := mm[0]
		if s.Pres != nil {
			p := s.Pres
			if p.Topic != "me" {
				t.Errorf("Uid %s: pres notify on topic is expected to be 'me', got %s", from.UserId(), p.Topic)
			}
			if p.SkipTopic != topicName {
				t.Errorf("Uid %s: pres skip topic is expected to be '%s', got %s", from.UserId(), topicName, p.SkipTopic)
			}
			if p.Src != topicName {
				t.Errorf("Uid %s: pres.src expected: %s, found: %s", from.UserId(), topicName, p.Src)
			}
			if p.What != "read" {
				t.Errorf("Uid %s: pres.what expected: 'read', found: %s", from.UserId(), p.What)
			}
		} else {
			t.Errorf("Uid %s: hub message expected to be {pres}.", from.UserId())
		}
	} else {
		t.Errorf("Uid %s: expected 1 hub message, got %d.", from.UserId(), len(mm))
	}
}

func TestHandleBroadcastPresMe(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	srcUid := types.Uid(10)
	helper.topic.perSubs = make(map[string]perSubsData)
	helper.topic.perSubs[srcUid.UserId()] = perSubsData{enabled: true, online: false}

	msg := &ServerComMessage{
		AsUser: uid.UserId(),
		RcptTo: uid.UserId(),
		Pres: &MsgServerPres{
			Topic: "me",
			Src:   srcUid.UserId(),
			What:  "on",
		},
	}
	helper.topic.handleServerMsg(msg)
	helper.finish()

	// Topic metadata.
	if online := helper.topic.perSubs[srcUid.UserId()].online; !online {
		t.Errorf("User %s is expected to be online.", srcUid.UserId())
	}
	// Server messages.
	if len(helper.results[0].messages) != 1 {
		t.Fatalf("Session 0 is expected to receive one message. Received %d.", len(helper.results[0].messages))
	}
	s := helper.results[0].messages[0].(*ServerComMessage)
	if s.RcptTo != uid.UserId() {
		t.Errorf("Message.RcptTo: expected '%s', found '%s'", uid.UserId(), s.RcptTo)
	}
	if s.Pres != nil {
		pres := s.Pres
		if pres.Topic != "me" {
			t.Errorf("Expected to notify user on 'me' topic. Found: '%s'", pres.Topic)
		}
		if pres.Src != srcUid.UserId() {
			t.Errorf("Expected notification from '%s'. Found: '%s'", srcUid.UserId(), pres.Topic)
		}
		if pres.What != "on" {
			t.Errorf("Expected an online notification. Found: '%s'", pres.What)
		}
	} else {
		t.Error("Message is expected to be pres.")
	}
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route isn't expected to receive messages. Received %d", len(helper.hubMessages))
	}
}

func TestHandleBroadcastPresInactiveTopic(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	srcUid := types.Uid(10)
	helper.topic.perSubs = make(map[string]perSubsData)
	helper.topic.perSubs[srcUid.UserId()] = perSubsData{enabled: true, online: false}

	msg := &ServerComMessage{
		AsUser: uid.UserId(),
		RcptTo: uid.UserId(),
		Pres: &MsgServerPres{
			Topic: "me",
			Src:   srcUid.UserId(),
			What:  "on",
		},
	}

	// Deactivate topic.
	helper.topic.markDeleted()

	helper.topic.handleServerMsg(msg)
	helper.finish()

	// Topic metadata.
	if online := helper.topic.perSubs[srcUid.UserId()].online; online {
		t.Errorf("User %s is expected to be offline.", srcUid.UserId())
	}
	// Server messages.
	if len(helper.results[0].messages) != 0 {
		t.Fatalf("Session 0 is not expected to receive messages. Received %d.", len(helper.results[0].messages))
	}
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route isn't expected to receive messages. Received %d", len(helper.hubMessages))
	}
}

const (
	NoSub               = 0
	ExistingSubEnabled  = 1
	ExistingSubDisabled = 2
)

func NoChangeInStatusTest(t *testing.T, subscriptionStatus int, what string) *TopicTestHelper {
	t.Helper()
	topicName := "usrMe"
	numUsers := 1
	helper := &TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, true)

	uid := helper.uids[0]
	srcUid := types.Uid(10)
	helper.topic.perSubs = make(map[string]perSubsData)
	enabled := false
	switch subscriptionStatus {
	case NoSub:
	case ExistingSubEnabled:
		enabled = true
		fallthrough
	case ExistingSubDisabled:
		helper.topic.perSubs[srcUid.UserId()] = perSubsData{enabled: enabled, online: false}
	}

	msg := &ServerComMessage{
		AsUser: uid.UserId(),
		RcptTo: uid.UserId(),
		Pres: &MsgServerPres{
			Topic: "me",
			Src:   srcUid.UserId(),
			// No change in online status.
			What: what,
		},
	}

	helper.topic.handleServerMsg(msg)
	helper.finish()

	// Topic metadata.
	if online := helper.topic.perSubs[srcUid.UserId()].online; online {
		t.Errorf("User %s is expected to be offline.", srcUid.UserId())
	}
	// Server messages.
	if len(helper.results[0].messages) != 0 {
		t.Fatalf("Session 0 is not expected to receive messages. Received %d.", len(helper.results[0].messages))
	}
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hubhelper.route isn't expected to receive messages. Received %d", len(helper.hubMessages))
	}
	return helper
}

func TestHandleBroadcastPresUnkn(t *testing.T) {
	NoChangeInStatusTest(t, ExistingSubEnabled, "?unkn").tearDown()
}

func TestHandleBroadcastPresNone(t *testing.T) {
	NoChangeInStatusTest(t, ExistingSubEnabled, "?none").tearDown()
}

func TestHandleBroadcastPresRedundantUpdate(t *testing.T) {
	h := NoChangeInStatusTest(t, ExistingSubDisabled, "off+rem")
	uid := h.uids[0]
	if _, ok := h.topic.perSubs[uid.UserId()]; ok {
		t.Errorf("Subscription for user %s expected to be deleted.", uid.UserId())
	}
	h.tearDown()
}

func TestHandleBroadcastPresNewSub(t *testing.T) {
	NoChangeInStatusTest(t, NoSub, "off+wrong").tearDown()
}

func TestHandleBroadcastPresUnknownSub(t *testing.T) {
	NoChangeInStatusTest(t, NoSub, "on+rem").tearDown()
}

func TestReplyGetDescInvalidOpts(t *testing.T) {
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, "" /*attach=*/, true)
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

// Verifies ctrl codes in session outputs.
func registerSessionVerifyOutputs(t *testing.T, sessionOutput *Responses, expectedCtrlCodes []int) {
	t.Helper()
	// Session output.
	if len(sessionOutput.messages) == len(expectedCtrlCodes) {
		n := len(expectedCtrlCodes)
		for i := 0; i < n; i++ {
			resp := sessionOutput.messages[i].(*ServerComMessage)
			code := expectedCtrlCodes[i]
			if resp.Ctrl != nil {
				if resp.Ctrl.Code != code {
					t.Errorf("response code: expected %d, found: %d", code, resp.Ctrl.Code)
				}
			} else {
				t.Errorf("response %d: expected to contain a Ctrl message", i)
			}
		}
	} else {
		t.Errorf("Session output: expected %d responses, received %d", len(expectedCtrlCodes),
			len(sessionOutput.messages))
	}
}

func TestRegisterSessionMe(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, false)
	defer helper.tearDown()
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
		join := &ClientComMessage{
			Sub: &MsgClientSub{
				Id:    fmt.Sprintf("id456-%d", i),
				Topic: "me",
			},
			AsUser: uid.UserId(),
			sess:   s,
		}
		helper.topic.registerSession(join)
	}
	helper.finish()

	if len(helper.topic.sessions) != 3 {
		t.Errorf("Attached sessions: expected 3, found %d", len(helper.topic.sessions))
	}
	for _, s := range helper.sessions {
		if len(s.subs) != 1 {
			t.Errorf("Session subscriptions: expected 3, found %d", len(s.subs))
		}
	}
	online := helper.topic.perUser[uid].online
	if online != 3 {
		t.Errorf("Number of online sessions: expected 3, found %d", online)
	}
	// Session output.
	for _, r := range helper.results {
		registerSessionVerifyOutputs(t, r, []int{http.StatusOK})
	}
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionInactiveTopic(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	uid := helper.uids[0]

	s := helper.sessions[0]
	join := &ClientComMessage{
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: "me",
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	// Deactivate topic.
	helper.topic.markDeleted()

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, helper.results[0], []int{http.StatusServiceUnavailable})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionUserSpecifiedInSetMessage(t *testing.T) {
	topicName := "grpTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	uid := helper.uids[0]

	s := helper.sessions[0]
	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					// Specify the user. This should result in an error.
					User: "foo",
				},
			},
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, helper.results[0], []int{http.StatusBadRequest})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionInvalidWantStrInSetMessage(t *testing.T) {
	topicName := "grpTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	uid := helper.uids[0]

	s := helper.sessions[0]
	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					// Specify the user. This should result in an error.
					Mode: "Invalid mode string",
				},
			},
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, helper.results[0], []int{http.StatusBadRequest})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionMaxSubscriberCountExceeded(t *testing.T) {
	topicName := "grpTest"
	// Pretend we already exceeded the maximum user count. This should produce an error.
	numUsers := 10
	oldMaxSubscribers := globals.maxSubscriberCount
	globals.maxSubscriberCount = 10
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer func() {
		helper.tearDown()
		globals.maxSubscriberCount = oldMaxSubscribers
	}()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	// New uid. This should attempt to add a new subscription.
	uid := types.Uid(10001)
	s, r := helper.newSession("test-sid", uid)
	helper.sessions = append(helper.sessions, s)
	helper.results = append(helper.results, r)

	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusUnprocessableEntity})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionLowAuthLevelWithSysTopic(t *testing.T) {
	topicName := "sys"
	// No one is subscribed to sys.
	numUsers := 0
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatSys, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	// New uid. This should attempt to add a new subscription
	// which produces an error b/c authLevel isn't root.
	uid := types.Uid(10001)
	s, r := helper.newSession("test-sid", uid)
	helper.sessions = append(helper.sessions, s)
	helper.results = append(helper.results, r)

	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusForbidden})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionNewChannelGetSubDbError(t *testing.T) {
	topicName := "grpTest"
	chanName := "chnTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	// It is a channel.
	helper.topic.isChan = true
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	// New uid. This should attempt to add a new subscription
	// which produces an error b/c authLevel isn't root.
	uid := types.Uid(10001)
	s, r := helper.newSession("test-sid", uid)
	helper.sessions = append(helper.sessions, s)
	helper.results = append(helper.results, r)

	join := &ClientComMessage{
		Original: chanName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: chanName,
		},
		AsUser: uid.UserId(),
		sess:   s,
	}

	helper.ss.EXPECT().Get(chanName, uid).Return(nil, types.ErrInternal)

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusInternalServerError})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionCreateSubFailed(t *testing.T) {
	topicName := "grpTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	// New uid. This should attempt to add a new subscription
	// which produces an error b/c authLevel isn't root.
	uid := types.Uid(10001)
	s, r := helper.newSession("test-sid", uid)
	helper.sessions = append(helper.sessions, s)
	helper.results = append(helper.results, r)

	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),
		sess:    s,
	}

	helper.ss.EXPECT().Create(gomock.Any()).Return(types.ErrInternal)

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusInternalServerError})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionAsChanUserNotChanSubcriber(t *testing.T) {
	topicName := "grpTest"
	chanName := "chnTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	// The topic is a channel.
	helper.topic.isChan = true
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	s := helper.sessions[0]
	uid := helper.uids[0]
	r := helper.results[0]

	// User is not a channel subscriber (userData.isChan is false).
	join := &ClientComMessage{
		Original: chanName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: chanName,
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),
		sess:    s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output. Tell the subscriber to use non-channel name.
	registerSessionVerifyOutputs(t, r, []int{http.StatusSeeOther})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionOwnerBansHimself(t *testing.T) {
	topicName := "grpTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	s := helper.sessions[0]
	uid := helper.uids[0]
	r := helper.results[0]

	// User is the topic owner.
	helper.topic.owner = uid
	pud := helper.topic.perUser[uid]
	pud.modeGiven |= types.ModeOwner
	helper.topic.perUser[uid] = pud

	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					// No O permission.
					Mode: "JPRW",
				},
			},
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),
		sess:    s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusForbidden})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionInvalidOwnershipTransfer(t *testing.T) {
	topicName := "grpTest"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	s := helper.sessions[1]
	uid := helper.uids[1]
	r := helper.results[1]

	// User is the topic owner.
	pud := helper.topic.perUser[uid]
	pud.modeWant = types.ModeCPublic
	pud.modeGiven = types.ModeCPublic
	helper.topic.perUser[uid] = pud

	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					// Want ownership.
					Mode: "JPRWSO",
				},
			},
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),
		sess:    s,
	}

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusForbidden})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionMetadataUpdateFails(t *testing.T) {
	topicName := "grpTest"
	numUsers := 2
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	s := helper.sessions[1]
	uid := helper.uids[1]
	r := helper.results[1]

	pud := helper.topic.perUser[uid]
	pud.modeWant = types.ModeCPublic
	pud.modeGiven = types.ModeCPublic
	helper.topic.perUser[uid] = pud

	// Want ownership.
	newWant := "JRWP"
	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					Mode: newWant,
				},
			},
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),

		sess: s,
	}
	// DB call fails.
	helper.ss.EXPECT().Update(topicName, uid, gomock.Any()).Return(types.ErrInternal)

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusInternalServerError})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestRegisterSessionOwnerChangeDbCallFails(t *testing.T) {
	topicName := "grpTest"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)
	defer helper.tearDown()
	if len(helper.topic.sessions) != 0 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 0 vs found %d", len(helper.topic.sessions))
	}

	s := helper.sessions[0]
	uid := helper.uids[0]
	r := helper.results[0]

	// User is the topic owner.
	pud := helper.topic.perUser[uid]
	pud.modeWant = types.ModeCPublic
	helper.topic.perUser[uid] = pud

	// Want ownership.
	newWant := "JRWPASO"
	join := &ClientComMessage{
		Original: topicName,
		Sub: &MsgClientSub{
			Id:    "id456",
			Topic: topicName,
			Set: &MsgSetQuery{
				Sub: &MsgSetSub{
					Mode: newWant,
				},
			},
		},
		AsUser:  uid.UserId(),
		AuthLvl: int(auth.LevelAuth),
		sess:    s,
	}
	helper.ss.EXPECT().Update(topicName, uid, gomock.Any()).Return(nil).Times(2)
	// OwnerChange call fails.
	helper.tt.EXPECT().OwnerChange(topicName, uid).Return(types.ErrInternal)

	helper.topic.registerSession(join)
	helper.finish()

	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	online := helper.topic.perUser[uid].online
	if online != 0 {
		t.Errorf("Number of online sessions: expected 0, found %d", online)
	}
	registerSessionVerifyOutputs(t, r, []int{})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestUnregisterSessionSimple(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	helper.uu.EXPECT().UpdateLastSeen(uid, gomock.Any(), gomock.Any()).Return(nil)

	// Add a couple more sessions.
	for i := 1; i < 3; i++ {
		s, r := helper.newSession(fmt.Sprintf("sid%d", i), uid)
		helper.sessions = append(helper.sessions, s)
		helper.results = append(helper.results, r)
		helper.topic.sessions[s] = perSessionData{uid: uid}
		pu := helper.topic.perUser[uid]
		pu.online++
		helper.topic.perUser[uid] = pu
	}

	// Initial online and attach session counts.
	if len(helper.topic.sessions) != 3 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 3 vs found %d", len(helper.topic.sessions))
	}
	if online := helper.topic.perUser[uid].online; online != 3 {
		t.Errorf("Number of online sessions: expected 3 vs found %d", online)
	}

	s := helper.sessions[0]
	r := helper.results[0]
	leave := &ClientComMessage{
		Leave: &MsgClientLeave{
			Id:    "id456",
			Topic: topicName,
		},
		AsUser: uid.UserId(),
		sess:   s,
		init:   true,
	}
	helper.topic.unregisterSession(leave)

	helper.finish()

	if len(helper.topic.sessions) != 2 {
		t.Errorf("Attached sessions: expected 2, found %d", len(helper.topic.sessions))
	}
	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(helper.sessions[0].subs))
	}
	if online := helper.topic.perUser[uid].online; online != 2 {
		t.Errorf("Number of online sessions after unregistering: expected 2, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusOK})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestUnregisterSessionInactiveTopic(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[0]

	// Initial online and attach session counts.
	if len(helper.topic.sessions) != 1 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 1 vs found %d", len(helper.topic.sessions))
	}
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("Number of online sessions: expected 1 vs found %d", online)
	}

	s := helper.sessions[0]
	r := helper.results[0]
	leave := &ClientComMessage{
		Leave: &MsgClientLeave{
			Id:    "id456",
			Topic: topicName,
		},
		AsUser: uid.UserId(),
		sess:   s,
		init:   true,
	}

	// Deactivate topic.
	helper.topic.markDeleted()

	helper.topic.unregisterSession(leave)
	helper.finish()

	if len(helper.topic.sessions) != 1 {
		t.Errorf("Attached sessions: expected 1, found %d", len(helper.topic.sessions))
	}
	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(s.subs))
	}
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("Number of online sessions after unregistering: expected 1, found %d", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusServiceUnavailable})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub isn't expected to receive any messages, received %d", len(helper.hubMessages))
	}
}

func TestUnregisterSessionUnsubscribe(t *testing.T) {
	topicName := "grpTest"
	numUsers := 3
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[2]
	helper.ss.EXPECT().Delete(topicName, uid).Return(nil)

	// Add a couple more sessions.
	for i := 0; i < 2; i++ {
		s, r := helper.newSession(fmt.Sprintf("sid-uid-%d-%d", uid, i), uid)
		helper.sessions = append(helper.sessions, s)
		helper.results = append(helper.results, r)
		helper.topic.sessions[s] = perSessionData{uid: uid}
		pu := helper.topic.perUser[uid]
		pu.online++
		helper.topic.perUser[uid] = pu
	}

	// Initial online and attach session counts.
	if len(helper.topic.sessions) != 5 {
		helper.finish()
		t.Fatalf("Initially attached sessions: expected 5 vs found %d", len(helper.topic.sessions))
	}
	if online := helper.topic.perUser[uid].online; online != 3 {
		t.Errorf("Number of online sessions: expected 3 vs found %d", online)
	}

	leave := &ClientComMessage{
		Leave: &MsgClientLeave{
			Id:    "id456",
			Topic: topicName,
			Unsub: true,
		},
		AsUser: uid.UserId(),
		sess:   helper.sessions[0],
		init:   true,
	}
	helper.topic.unregisterSession(leave)
	helper.finish()

	if len(helper.topic.sessions) != 2 {
		t.Errorf("Attached sessions: expected 2, found %d", len(helper.topic.sessions))
	}
	if len(helper.sessions[0].subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(helper.sessions[0].subs))
	}
	if pu, ok := helper.topic.perUser[uid]; pu.online != 0 || ok {
		t.Errorf("Number of online sessions after unsubscribing: expected 2, found %d; perUser entry found: %t", pu.online, ok)
	}
	// Session output. Sessions 2, 3, 4 are the evicted/unsubscribed uid.
	for i := 2; i < 5; i++ {
		r := helper.results[i]
		registerSessionVerifyOutputs(t, r, []int{http.StatusResetContent})
	}
	// Presence notifications.
	if len(helper.hubMessages) != 2 {
		t.Errorf("Hub messages recipients: expected 2, received %d", len(helper.hubMessages))
	}
	// Group presSubs.
	if grpPres, ok := helper.hubMessages[topicName]; ok {
		if len(grpPres) != 2 {
			t.Fatalf("Group presence messages: expected 2, got %d", len(grpPres))
		}
		for _, msg := range grpPres {
			//
			pres := msg.Pres
			if pres == nil {
				t.Fatal("Presence message expected in hub output, but not found.")
			}
			if pres.Topic != topicName {
				t.Errorf("Presence message topic: expected %s, found %s", topicName, pres.Topic)
			}
			if pres.Src != uid.UserId() {
				t.Errorf("Presence message src: expected %s, found %s", uid.UserId(), pres.Src)
			}
			if pres.What != "acs" && pres.What != "off" {
				t.Errorf("Presence message what: expected 'acs' or 'off', found %s", pres.What)
			}
		}
	} else {
		t.Errorf("Hub expected to pres recipient %s", topicName)
	}
	// User notification.
	if userPres, ok := helper.hubMessages[uid.UserId()]; ok {
		if len(userPres) != 1 {
			t.Fatalf("User presence messages: expected 1, got %d", len(userPres))
		}
		pres := userPres[0].Pres
		if pres == nil {
			t.Fatal("Presence message expected in hub output, but not found.")
		}
		if pres.Topic != "me" {
			t.Errorf("Presence message topic: expected 'me', found %s", pres.Topic)
		}
		if pres.Src != topicName {
			t.Errorf("Presence message src: expected %s, found %s", topicName, pres.Src)
		}
		if pres.What != "gone" {
			t.Errorf("Presence message what: expected 'gone', found %s", pres.What)
		}
	} else {
		t.Errorf("Hub expected to pres recipient %s", uid.UserId())
	}
}

func TestUnregisterSessionOwnerCannotUnsubscribe(t *testing.T) {
	topicName := "grpTest"
	numUsers := 3
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	s := helper.sessions[0]
	r := helper.results[0]

	leave := &ClientComMessage{
		Leave: &MsgClientLeave{
			Id:    "id456",
			Topic: topicName,
			Unsub: true,
		},
		AsUser: uid.UserId(),
		sess:   s,
		init:   true,
	}

	helper.topic.unregisterSession(leave)
	helper.finish()

	if len(helper.topic.sessions) != 3 {
		t.Errorf("Attached sessions: expected 3, found %d", len(helper.topic.sessions))
	}
	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(helper.sessions[0].subs))
	}
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("Number of online sessions after failed unsubscribing: expected 1, found %d.", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusForbidden})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub messages recipients: expected 0, received %d", len(helper.hubMessages))
	}
}

func TestUnregisterSessionUnsubDeleteCallFails(t *testing.T) {
	topicName := "grpTest"
	numUsers := 3
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, true)
	defer helper.tearDown()

	// Unsubscribe user 1 (cannot unsub user 0, the owner).
	uid := helper.uids[1]
	s := helper.sessions[1]
	r := helper.results[1]

	leave := &ClientComMessage{
		Leave: &MsgClientLeave{
			Id:    "id456",
			Topic: topicName,
			Unsub: true,
		},
		AsUser: uid.UserId(),
		sess:   s,
		init:   true,
	}
	// DB call fails.
	helper.ss.EXPECT().Delete(topicName, uid).Return(types.ErrInternal)

	helper.topic.unregisterSession(leave)
	helper.finish()

	if len(helper.topic.sessions) != 3 {
		t.Errorf("Attached sessions: expected 3, found %d", len(helper.topic.sessions))
	}
	if len(s.subs) != 0 {
		t.Errorf("Session subscriptions: expected 0, found %d", len(helper.sessions[0].subs))
	}
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("Number of online sessions after failed unsubscribing: expected 1, found %d.", online)
	}
	// Session output.
	registerSessionVerifyOutputs(t, r, []int{http.StatusInternalServerError})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub messages recipients: expected 0, received %d", len(helper.hubMessages))
	}
}

func TestHandleMetaChanErr(t *testing.T) {
	topicName := "grpTest"
	chanName := "chnTest"
	numUsers := 3
	helper := TopicTestHelper{}
	defer helper.tearDown()
	helper.setUp(t, numUsers, types.TopicCatGrp, topicName, false)

	// This is not a channel. However, we will try to handle an info message where
	// the topic is referenced as "chn".
	helper.topic.isChan = false
	// Empty message since this request should trigger an error anyway.
	meta := &ClientComMessage{
		AsUser:   helper.uids[0].UserId(),
		Original: chanName,
		MetaWhat: constMsgMetaDesc | constMsgMetaSub | constMsgMetaData | constMsgMetaDel,
		sess:     helper.sessions[0],
	}
	helper.topic.handleMeta(meta)
	helper.finish()

	// Session output.
	registerSessionVerifyOutputs(t, helper.results[0], []int{http.StatusNotFound})
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub messages recipients: expected 0, received %d", len(helper.hubMessages))
	}
}

func TestHandleMetaGet(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	helper.mm.EXPECT().GetAll(topicName, uid, gomock.Any()).Return([]types.Message{}, nil)
	helper.mm.EXPECT().GetDeleted(topicName, uid, gomock.Any()).Return([]types.Range{}, 0, nil)
	helper.uu.EXPECT().GetTopics(uid, gomock.Any()).Return([]types.Subscription{}, nil)

	meta := &ClientComMessage{
		Get: &MsgClientGet{
			Id:    "id456",
			Topic: topicName,
			MsgGetQuery: MsgGetQuery{
				What: "desc sub data del",
				Desc: &MsgGetOpts{},
				Sub:  &MsgGetOpts{},
				Data: &MsgGetOpts{},
				Del:  &MsgGetOpts{},
			},
		},
		AsUser:   uid.UserId(),
		MetaWhat: constMsgMetaDesc | constMsgMetaSub | constMsgMetaData | constMsgMetaDel,
		sess:     helper.sessions[0],
	}
	helper.topic.handleMeta(meta)
	helper.finish()

	r := helper.results[0]
	if len(r.messages) != 4 {
		t.Errorf("Responses received: expected 4, received %d", len(r.messages))
	}
	for _, msg := range r.messages {
		m := msg.(*ServerComMessage)
		if m.Meta != nil {
			if m.Meta.Desc == nil {
				t.Error("Meta.Desc expected to be specified.")
			}
		} else if m.Ctrl == nil {
			t.Error("Expected only meta or ctrl messages.")
		}
	}
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Errorf("Hub messages recipients: expected 0, received %d", len(helper.hubMessages))
	}
}

// Matches a subset in a superset.
type supersetOf struct{ subset map[string]string }

func SupersetOf(subset map[string]string) gomock.Matcher {
	return &supersetOf{subset}
}

func (s *supersetOf) Matches(x interface{}) bool {
	super := x.(map[string]interface{})
	if super == nil {
		return false
	}
	for k, v := range s.subset {
		if x, ok := super[k]; ok {
			val := x.(string)
			if val != v {
				return false
			}
		} else {
			return false
		}
	}
	return true
}

func (s *supersetOf) String() string {
	return fmt.Sprintf("%+v is subset", s.subset)
}

func TestHandleMetaSetDescMePublicPrivate(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	gomock.InOrder(
		helper.uu.EXPECT().Update(uid, SupersetOf(map[string]string{"Public": "new public"})).Return(nil),
		helper.ss.EXPECT().Update(topicName, uid, map[string]interface{}{"Private": "new private"}).Return(nil),
	)

	meta := &ClientComMessage{
		Set: &MsgClientSet{
			Id:    "id456",
			Topic: topicName,
			MsgSetQuery: MsgSetQuery{
				Desc: &MsgSetDesc{
					Public:  "new public",
					Private: "new private",
				},
			},
		},
		AsUser:   uid.UserId(),
		MetaWhat: constMsgMetaDesc,
		sess:     helper.sessions[0],
	}
	helper.topic.handleMeta(meta)
	helper.finish()

	r := helper.results[0]
	if len(r.messages) != 1 {
		t.Fatalf("Responses received: expected 1, received %d", len(r.messages))
	}
	msg := r.messages[0].(*ServerComMessage)
	if msg == nil || msg.Ctrl == nil {
		t.Fatalf("Server message expected to have a ctrl submessage: %+v", msg)
	}
	if msg.Ctrl.Code != 200 {
		t.Errorf("Response code: expected 200, found %d", msg.Ctrl.Code)
	}
	// Presence notifications.
	if len(helper.hubMessages) != 1 {
		t.Fatalf("Hub messages recipients: expected 1, received %d", len(helper.hubMessages))
	}
	// Make sure uid's sessions are notified.
	if userPres, ok := helper.hubMessages[uid.UserId()]; ok {
		if len(userPres) != 1 {
			t.Fatalf("User presence messages: expected 1, got %d", len(userPres))
		}
		if userPres[0].SkipSid != helper.sessions[0].sid {
			t.Errorf("Pres notification SkipSid: %s expected vs %s found", helper.sessions[0].sid, userPres[0].SkipSid)
		}
		pres := userPres[0].Pres
		if pres == nil {
			t.Fatal("Presence message expected in hub output, but not found.")
		}
		if pres.Topic != "me" {
			t.Errorf("Presence message topic: expected 'me', found %s", pres.Topic)
		}
		if pres.What != "upd" {
			t.Errorf("Presence message what: expected 'upd', found %s", pres.What)
		}
	} else {
		t.Errorf("Hub expected to pres recipient %s", uid.UserId())
	}
}

func TestHandleSessionUpdateSessToForeground(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	supd := &sessionUpdate{
		sess: helper.sessions[0],
	}
	var uaAgent string
	helper.topic.handleSessionUpdate(supd, &uaAgent, nil)
	helper.finish()

	// Expect online count bumped up to 2.
	if online := helper.topic.perUser[uid].online; online != 2 {
		t.Errorf("online count for %s: expected 2, found %d", uid.UserId(), online)
	}
}

func TestHandleSessionUpdateUserAgent(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	supd := &sessionUpdate{
		userAgent: "newUA",
	}
	uaAgent := "oldUA"
	timer := time.NewTimer(time.Hour)
	helper.topic.handleSessionUpdate(supd, &uaAgent, timer)
	helper.finish()

	// online count stays 1.
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("online count for %s: expected 1, found %d", uid.UserId(), online)
	}
	if uaAgent != "newUA" {
		t.Errorf("User agent: expected 'newUA', found '%s'", uaAgent)
	}
	timer.Stop()
}

func TestHandleUATimerEvent(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	helper.topic.perSubs = make(map[string]perSubsData)
	helper.topic.perSubs[uid.UserId()] = perSubsData{online: true}
	helper.topic.handleUATimerEvent("newUA")
	helper.finish()

	if helper.topic.userAgent != "newUA" {
		t.Errorf("Topic's user agent: expected 'newUA', found '%s'", helper.topic.userAgent)
	}
	// Presence notifications.
	if len(helper.hubMessages) != 1 {
		t.Fatalf("Hub messages recipients: expected 1, received %d", len(helper.hubMessages))
	}
	// Make sure uid's sessions are notified.
	if userPres, ok := helper.hubMessages[uid.UserId()]; ok {
		if len(userPres) != 1 {
			t.Fatalf("User presence messages: expected 1, got %d", len(userPres))
		}
		pres := userPres[0].Pres
		if pres == nil {
			t.Fatal("Presence message expected in hub output, but not found.")
		}
		if pres.Topic != "me" {
			t.Errorf("Presence message topic: expected 'me', found '%s'", pres.Topic)
		}
		if pres.What != "ua" {
			t.Errorf("Presence message what: expected 'ua', found '%s'", pres.What)
		}
		if pres.Src != topicName {
			t.Errorf("Presence message src: expected '%s', found '%s'", topicName, pres.Src)
		}
	} else {
		t.Errorf("Hub expected to pres recipient %s", uid.UserId())
	}
}

func TestHandleTopicTimeout(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	uid := helper.uids[0]
	helper.topic.perSubs = make(map[string]perSubsData)
	helper.topic.perSubs[uid.UserId()] = perSubsData{online: true}
	helper.hub.unreg = make(chan *topicUnreg, 10)
	uaTimer := time.NewTimer(time.Hour)
	notifTimer := time.NewTimer(time.Hour)
	helper.topic.handleTopicTimeout(helper.hub, "newUA", uaTimer, notifTimer)
	helper.finish()

	if len(helper.hub.unreg) != 1 {
		t.Fatalf("Hub.unreg chan must contain exactly 1 message. Found %d.", len(helper.hub.unreg))
	}
	if unreg := <-helper.hub.unreg; unreg.rcptTo != topicName {
		t.Errorf("unreg.rcptTo: expected '%s', found '%s'", topicName, unreg.rcptTo)
	}
	uaTimer.Stop()
	notifTimer.Stop()
	// Presence notifications.
	if len(helper.hubMessages) != 1 {
		t.Fatalf("Hub messages recipients: expected 1, received %d", len(helper.hubMessages))
	}
	// Make sure uid's sessions are notified.
	if userPres, ok := helper.hubMessages[uid.UserId()]; ok {
		if len(userPres) != 1 {
			t.Fatalf("User presence messages: expected 1, got %d", len(userPres))
		}
		pres := userPres[0].Pres
		if pres == nil {
			t.Fatal("Presence message expected in hub output, but not found.")
		}
		if pres.Topic != "me" {
			t.Errorf("Presence message topic: expected 'me', found '%s'", pres.Topic)
		}
		if pres.What != "off" {
			t.Errorf("Presence message what: expected 'off', found '%s'", pres.What)
		}
		if pres.Src != topicName {
			t.Errorf("Presence message src: expected '%s', found '%s'", topicName, pres.Src)
		}
	} else {
		t.Errorf("Hub expected to pres recipient %s", uid.UserId())
	}
}

func TestHandleTopicTermination(t *testing.T) {
	topicName := "usrMe"
	numUsers := 1
	helper := TopicTestHelper{}
	helper.setUp(t, numUsers, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	done := make(chan bool, 1)
	exit := &shutDown{
		reason: StopDeleted,
		done:   done,
	}
	helper.topic.handleTopicTermination(exit)
	helper.finish()

	if len(done) != 1 {
		t.Fatal("done callback isn't invoked.")
	}
	<-done
	for i, s := range helper.sessions {
		if len(s.detach) != 1 {
			t.Fatalf("Session %d: detach channel is empty.", i)
		}
		val := <-s.detach
		if val != topicName {
			t.Errorf("Session %d is expected to detach from topic '%s', found '%s'.", i, topicName, val)
		}
	}
	// Presence notifications.
	if len(helper.hubMessages) != 0 {
		t.Fatalf("Hub messages recipients: expected 0, received %d", len(helper.hubMessages))
	}
}

func TestMain(m *testing.M) {
	logs.Init(os.Stderr, "stdFlags")
	// Set max subscriber count to effective infinity.
	globals.maxSubscriberCount = 1000000000
	os.Exit(m.Run())
}
