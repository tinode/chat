package main

import (
	"net/http"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

// Owner's access to 'me' in incognito mode: the default "JPS" without P.
const modeMeIncognito = types.ModeCMeFnd &^ types.ModePres

// newMeTopicForPresTest creates a 'me' topic of user 1 with the given access mode of the owner
// and points the hub to a buffered channel which collects routed presence notifications.
func newMeTopicForPresTest(t *testing.T, ownerWant types.AccessMode) (*Topic, chan *ServerComMessage) {
	t.Helper()
	routeSrv := make(chan *ServerComMessage, 10)
	globals.hub = &Hub{routeSrv: routeSrv}
	t.Cleanup(func() { globals.hub = nil })

	owner := types.Uid(1)
	topic := &Topic{
		name:    owner.UserId(),
		cat:     types.TopicCatMe,
		perUser: map[types.Uid]perUserData{owner: {modeWant: ownerWant, modeGiven: types.ModeCMeFnd}},
		perSubs: make(map[string]perSubsData),
	}
	return topic, routeSrv
}

// drainRouted returns all messages routed so far without blocking.
func drainRouted(routeSrv chan *ServerComMessage) []*ServerComMessage {
	var msgs []*ServerComMessage
	for {
		select {
		case msg := <-routeSrv:
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

func TestIsIncognito(t *testing.T) {
	visible, _ := newMeTopicForPresTest(t, types.ModeCMeFnd)
	if visible.isIncognito() {
		t.Error("'me' with P: expected visible")
	}

	incognito, _ := newMeTopicForPresTest(t, modeMeIncognito)
	if !incognito.isIncognito() {
		t.Error("'me' without P: expected incognito")
	}

	notLoaded, _ := newMeTopicForPresTest(t, modeMeIncognito)
	notLoaded.perUser = map[types.Uid]perUserData{}
	if notLoaded.isIncognito() {
		t.Error("owner's subscription is not loaded: expected visible")
	}

	group := &Topic{name: "grpTest", cat: types.TopicCatGrp,
		perUser: map[types.Uid]perUserData{types.Uid(1): {modeWant: modeMeIncognito, modeGiven: modeMeIncognito}}}
	if group.isIncognito() {
		t.Error("non-'me' topic: expected visible")
	}
}

func TestProcPresReqIncognitoDoesNotReplyOnline(t *testing.T) {
	contact := types.Uid(2).UserId()
	tests := []struct {
		name      string
		ownerWant types.AccessMode
		what      string
		wantReply bool
	}{
		// A contact came online and asks for the owner's status.
		{"contact online, visible", types.ModeCMeFnd, "on", true},
		{"contact online, incognito", modeMeIncognito, "on", false},
		// A new P2P conversation initiates exchange of statuses.
		{"new conversation, visible", types.ModeCMeFnd, "?unkn+en", true},
		{"new conversation, incognito", modeMeIncognito, "?unkn+en", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			topic, routeSrv := newMeTopicForPresTest(t, tc.ownerWant)
			topic.procPresReq(contact, tc.what, true)

			msgs := drainRouted(routeSrv)
			if !tc.wantReply {
				if len(msgs) != 0 {
					t.Fatalf("expected no reply, got %d: %+v", len(msgs), msgs[0].Pres)
				}
				return
			}
			if len(msgs) != 1 {
				t.Fatalf("expected 1 reply, got %d", len(msgs))
			}
			if msgs[0].RcptTo != contact || msgs[0].Pres.What != "on" {
				t.Errorf("expected 'on' to %s, got '%s' to %s", contact, msgs[0].Pres.What, msgs[0].RcptTo)
			}
		})
	}
}

func TestSendSubNotificationsIncognitoStaysOffline(t *testing.T) {
	owner, contact := types.Uid(1), types.Uid(2)
	tests := []struct {
		name      string
		ownerWant types.AccessMode
		wantOn    bool
	}{
		{"visible", types.ModeCMeFnd, true},
		{"incognito", modeMeIncognito, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			users := mock_store.NewMockUsersPersistenceInterface(ctrl)
			store.Users = users
			t.Cleanup(func() { store.Users = nil })
			users.EXPECT().GetSubs(owner).Return([]types.Subscription{{
				Topic:     owner.P2PName(contact),
				ModeWant:  types.ModeCP2P,
				ModeGiven: types.ModeCP2P,
			}}, nil)

			// First session attaches to a not yet loaded 'me': the user is coming online.
			topic, routeSrv := newMeTopicForPresTest(t, tc.ownerWant)
			topic.sendSubNotifications(owner, "sid", "ua")

			if !topic.isLoaded() {
				t.Error("topic is expected to be marked as loaded")
			}
			msgs := drainRouted(routeSrv)
			if !tc.wantOn {
				if len(msgs) != 0 {
					t.Fatalf("expected no notifications, got %d: %+v", len(msgs), msgs[0].Pres)
				}
				return
			}
			if len(msgs) != 1 || msgs[0].RcptTo != contact.UserId() || msgs[0].Pres.What != "on" {
				t.Fatalf("expected a single 'on' to %s, got %d messages", contact.UserId(), len(msgs))
			}
		})
	}
}

func TestHandleUATimerEventIncognito(t *testing.T) {
	contact := types.Uid(2).UserId()
	tests := []struct {
		name      string
		ownerWant types.AccessMode
		wantUA    bool
	}{
		{"visible", types.ModeCMeFnd, true},
		{"incognito", modeMeIncognito, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			topic, routeSrv := newMeTopicForPresTest(t, tc.ownerWant)
			topic.userAgent = "old-ua"
			topic.perSubs[contact] = perSubsData{online: true, enabled: true}

			topic.handleUATimerEvent("new-ua")

			if topic.userAgent != "new-ua" {
				t.Errorf("user agent expected to be updated, found %q", topic.userAgent)
			}
			msgs := drainRouted(routeSrv)
			if !tc.wantUA {
				if len(msgs) != 0 {
					t.Fatalf("expected no notifications, got %d: %+v", len(msgs), msgs[0].Pres)
				}
				return
			}
			if len(msgs) != 1 || msgs[0].RcptTo != contact || msgs[0].Pres.What != "ua" {
				t.Fatalf("expected a single 'ua' to %s, got %d messages", contact, len(msgs))
			}
		})
	}
}

func TestUnregisterSessionIncognitoKeepsLastSeen(t *testing.T) {
	uid := types.Uid(1)
	topicName := uid.UserId()
	helper := TopicTestHelper{}
	helper.setUp(t, 1, types.TopicCatMe, topicName /*attach=*/, true)
	defer helper.tearDown()

	// Owner is in incognito mode. No UpdateLastSeen is expected: gomock fails the test on any call.
	pu := helper.topic.perUser[uid]
	pu.modeWant = modeMeIncognito
	helper.topic.perUser[uid] = pu

	// Second session, so the topic stays online after the first one leaves.
	s, r := helper.newSession("sid1", uid)
	helper.sessions = append(helper.sessions, s)
	helper.results = append(helper.results, r)
	helper.topic.sessions[s] = perSessionData{uid: uid}
	pu = helper.topic.perUser[uid]
	pu.online++
	helper.topic.perUser[uid] = pu

	leave := &ClientComMessage{
		Leave:  &MsgClientLeave{Id: "id456", Topic: topicName},
		AsUser: uid.UserId(),
		sess:   helper.sessions[0],
		init:   true,
	}
	helper.topic.unregisterSession(leave)

	helper.finish()
	if errorMsgs, hasError := helper.hubMessages["__ERROR__"]; hasError {
		t.Fatal(errorMsgs[0].Ctrl.Text)
	}
	registerSessionVerifyOutputs(t, helper.results[0], []int{http.StatusOK})
	if online := helper.topic.perUser[uid].online; online != 1 {
		t.Errorf("online sessions: expected 1, found %d", online)
	}
}
