package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/auth/mock_auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

// Basic logins, 'uname:password'.
const (
	testRootLogin  = "cos:root-password"
	testUserLogin  = "alice:password"
	testWrongLogin = "cos:wrong-password"
)

// avatarTestHelper replaces the store and the hub, the hub's routeSrv only collects messages.
type avatarTestHelper struct {
	ctrl *gomock.Controller
	uu   *mock_store.MockUsersPersistenceInterface
	tt   *mock_store.MockTopicsPersistenceInterface
	hub  *Hub

	// Replaced globals, restored by tearDown. Other tests use the default store.Store.
	restore func()
}

func (b *avatarTestHelper) setUp(t *testing.T) {
	t.Helper()
	hub, apiKeySalt := globals.hub, globals.apiKeySalt
	storeObj, users, topics := store.Store, store.Users, store.Topics
	b.restore = func() {
		globals.hub, globals.apiKeySalt = hub, apiKeySalt
		store.Store, store.Users, store.Topics = storeObj, users, topics
	}

	b.ctrl = gomock.NewController(t)
	b.uu = mock_store.NewMockUsersPersistenceInterface(b.ctrl)
	b.tt = mock_store.NewMockTopicsPersistenceInterface(b.ctrl)
	store.Users = b.uu
	store.Topics = b.tt
	b.hub = &Hub{routeSrv: make(chan *ServerComMessage, 20)}
	globals.hub = b.hub
}

// setUpAuth accepts the API key from testAPIKey and the three test logins.
func (b *avatarTestHelper) setUpAuth() {
	globals.apiKeySalt = []byte("test-api-key-salt")
	authhdl := mock_auth.NewMockAuthHandler(b.ctrl)
	authhdl.EXPECT().Authenticate([]byte(testRootLogin), gomock.Any()).
		Return(&auth.Rec{Uid: types.Uid(100), AuthLevel: auth.LevelRoot}, nil, nil).AnyTimes()
	authhdl.EXPECT().Authenticate([]byte(testUserLogin), gomock.Any()).
		Return(&auth.Rec{Uid: types.Uid(5), AuthLevel: auth.LevelAuth}, nil, nil).AnyTimes()
	authhdl.EXPECT().Authenticate([]byte(testWrongLogin), gomock.Any()).
		Return(nil, nil, types.ErrFailed).AnyTimes()
	st := mock_store.NewMockPersistentStorageInterface(b.ctrl)
	st.EXPECT().GetLogicalAuthHandler("Basic").Return(authhdl).AnyTimes()
	store.Store = st
}

func (b *avatarTestHelper) tearDown() {
	b.restore()
	b.ctrl.Finish()
}

// routed returns the messages routed so far, keyed by recipient.
func (b *avatarTestHelper) routed(t *testing.T) map[string]*ServerComMessage {
	t.Helper()
	result := make(map[string]*ServerComMessage)
	for len(b.hub.routeSrv) > 0 {
		msg := <-b.hub.routeSrv
		if _, ok := result[msg.RcptTo]; ok {
			t.Errorf("%s is notified more than once", msg.RcptTo)
		}
		result[msg.RcptTo] = msg
	}
	return result
}

// testAPIKey signs an API key with globals.apiKeySalt, as the keygen tool does.
func testAPIKey() string {
	data := make([]byte, apikeyLength)
	data[0] = 1
	signed := apikeyVersion + apikeyAppID + apikeySequence + apikeyWho
	hasher := hmac.New(md5.New, globals.apiKeySalt)
	hasher.Write(data[:signed])
	copy(data[signed:], hasher.Sum(nil))
	return base64.URLEncoding.EncodeToString(data)
}

func accessMode(t *testing.T, s string) types.AccessMode {
	t.Helper()
	var mode types.AccessMode
	if err := mode.UnmarshalText([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return mode
}

func subscription(t *testing.T, user types.Uid, topic, given string) types.Subscription {
	t.Helper()
	return types.Subscription{
		User:      user.String(),
		Topic:     topic,
		ModeWant:  types.ModeCP2P,
		ModeGiven: accessMode(t, given),
	}
}

func avatarUpdateBody(chatID string, userID int64, version int) string {
	return fmt.Sprintf(`{"chat_id":%q,"user_id":%d,"avatar_version":%d}`, chatID, userID, version)
}

func postAvatarUpdate(method, apiKey, login, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v0/internal/avatar-updated", strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("X-Tinode-APIKey", apiKey)
	}
	if login != "" {
		req.Header.Set("X-Tinode-Auth", "Basic "+base64.StdEncoding.EncodeToString([]byte(login)))
	}
	rec := httptest.NewRecorder()
	serveAvatarUpdated(rec, req)
	return rec
}

func TestServeAvatarUpdatedRejectsInvalidRequests(t *testing.T) {
	// No store expectations: a rejected request must not announce anything.
	helper := avatarTestHelper{}
	helper.setUp(t)
	helper.setUpAuth()
	defer helper.tearDown()

	user := types.Uid(1).UserId()
	key := testAPIKey()
	valid := avatarUpdateBody(user, 42, 7)
	cases := []struct {
		name   string
		method string
		apiKey string
		login  string
		body   string
		code   int
	}{
		{"wrong method", http.MethodGet, key, testRootLogin, valid, http.StatusMethodNotAllowed},
		{"missing api key", http.MethodPost, "", testRootLogin, valid, http.StatusForbidden},
		{"invalid api key", http.MethodPost, "AQAAAAABAACkeyIsNotSignedProperly", testRootLogin, valid, http.StatusForbidden},
		{"missing login", http.MethodPost, key, "", valid, http.StatusUnauthorized},
		{"wrong password", http.MethodPost, key, testWrongLogin, valid, http.StatusUnauthorized},
		{"not a root user", http.MethodPost, key, testUserLogin, valid, http.StatusForbidden},
		{"malformed json", http.MethodPost, key, testRootLogin, "{", http.StatusBadRequest},
		{"group instead of user", http.MethodPost, key, testRootLogin, avatarUpdateBody("grpabc", 42, 7), http.StatusBadRequest},
		{"missing user id", http.MethodPost, key, testRootLogin, avatarUpdateBody(user, 0, 7), http.StatusBadRequest},
		{"missing version", http.MethodPost, key, testRootLogin, avatarUpdateBody(user, 42, 0), http.StatusBadRequest},
		{"too large", http.MethodPost, key, testRootLogin,
			strings.TrimSuffix(valid, "}") + `,"pad":"` + strings.Repeat("a", maxInternalRequestSize) + `"}`,
			http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAvatarUpdate(tc.method, tc.apiKey, tc.login, tc.body)

			if rec.Code != tc.code {
				t.Errorf("status = %d, want %d", rec.Code, tc.code)
			}
			var resp ServerComMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Ctrl == nil || resp.Ctrl.Code != tc.code {
				t.Errorf("body = %s, want a {ctrl} with code %d", rec.Body.String(), tc.code)
			}
		})
	}
	if len(helper.hub.routeSrv) != 0 {
		t.Errorf("a rejected request announced %d messages", len(helper.hub.routeSrv))
	}
}

func TestServeAvatarUpdatedAnnouncesTheChange(t *testing.T) {
	helper := avatarTestHelper{}
	helper.setUp(t)
	helper.setUpAuth()
	defer helper.tearDown()

	user := types.Uid(1)
	helper.uu.EXPECT().GetSubs(user).Return(nil, nil)

	rec := postAvatarUpdate(http.MethodPost, testAPIKey(), testRootLogin, avatarUpdateBody(user.UserId(), 42, 7))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var msg *ServerComMessage
	select {
	case msg = <-helper.hub.routeSrv:
	case <-time.After(time.Second):
		t.Fatal("avatar update was not announced")
	}
	if msg.RcptTo != user.UserId() {
		t.Errorf("RcptTo = %s, want the user's own 'me' %s", msg.RcptTo, user.UserId())
	}
	if info := msg.Info; info == nil || info.Topic != "me" || info.Src != user.UserId() ||
		info.From != user.UserId() || info.What != "avatar" {
		t.Errorf("info = %+v, want {info topic=me src=from=%s what=avatar}", info, user.UserId())
	}
	if payload := string(msg.Info.Payload); payload != `{"type":"profile.avatar_updated","user_id":42,"avatar_version":7}` {
		t.Errorf("payload = %s", payload)
	}
}

func TestAnnounceAvatarUpdatedReachesPeersAndGroupMembers(t *testing.T) {
	helper := avatarTestHelper{}
	helper.setUp(t)
	defer helper.tearDown()

	user, peer, blockedPeer, blockingPeer := types.Uid(1), types.Uid(2), types.Uid(3), types.Uid(4)
	member, bannedMember := types.Uid(5), types.Uid(6)

	p2p := user.P2PName(peer)
	p2pBlocked := user.P2PName(blockedPeer)
	p2pBlocking := user.P2PName(blockingPeer)
	// Hotspot group chats give members RWP.
	group, failingGroup, channel := "grpHotspot", "grpFailing", "chnNews"

	helper.uu.EXPECT().GetSubs(user).Return([]types.Subscription{
		subscription(t, user, user.UserId(), "JRWPA"),
		subscription(t, user, user.FndName(), "JRWPA"),
		subscription(t, user, p2p, "JRWPA"),
		subscription(t, user, p2pBlocked, "JRWPA"),
		// The peer blocked the user: the user's own subscription keeps only JR.
		subscription(t, user, p2pBlocking, "JR"),
		subscription(t, user, group, "RWP"),
		subscription(t, user, failingGroup, "RWP"),
		subscription(t, user, channel, "JRWPA"),
	}, nil)
	helper.tt.EXPECT().GetSubs(p2p, nil).Return([]types.Subscription{
		subscription(t, user, p2p, "JRWPA"),
		subscription(t, peer, p2p, "JRWPA"),
	}, nil)
	helper.tt.EXPECT().GetSubs(p2pBlocked, nil).Return([]types.Subscription{
		subscription(t, user, p2pBlocked, "JRWPA"),
		// The user blocked this peer.
		subscription(t, blockedPeer, p2pBlocked, "JR"),
	}, nil)
	helper.tt.EXPECT().GetSubs(group, nil).Return([]types.Subscription{
		subscription(t, user, group, "RWP"),
		subscription(t, peer, group, "RWP"),
		subscription(t, member, group, "RWP"),
		subscription(t, bannedMember, group, "N"),
	}, nil)
	helper.tt.EXPECT().GetSubs(failingGroup, nil).Return(nil, errors.New("db down"))
	// No GetSubs for p2pBlocking or the channel: gomock fails the test if they're loaded.

	payload := json.RawMessage(`{"type":"profile.avatar_updated","user_id":42,"avatar_version":7}`)
	announceAvatarUpdated(user, payload)

	routed := helper.routed(t)
	for _, uid := range []types.Uid{user, peer, member} {
		msg, ok := routed[uid.UserId()]
		if !ok {
			t.Errorf("%s is not notified", uid.UserId())
			continue
		}
		if msg.Info == nil || msg.Info.Topic != "me" || msg.Info.Src != user.UserId() ||
			msg.Info.What != "avatar" || string(msg.Info.Payload) != string(payload) {
			t.Errorf("%s got %+v", uid.UserId(), msg.Info)
		}
	}
	for _, uid := range []types.Uid{blockedPeer, blockingPeer, bannedMember} {
		if _, ok := routed[uid.UserId()]; ok {
			t.Errorf("%s should not be notified", uid.UserId())
		}
	}
	if len(routed) != 3 {
		t.Errorf("notified %d users, want 3", len(routed))
	}
}

func TestAnnounceAvatarUpdatedStillReachesOwnSessionsWhenChatsFailToLoad(t *testing.T) {
	helper := avatarTestHelper{}
	helper.setUp(t)
	defer helper.tearDown()

	user := types.Uid(1)
	helper.uu.EXPECT().GetSubs(user).Return(nil, errors.New("db down"))

	announceAvatarUpdated(user, json.RawMessage(`{}`))

	routed := helper.routed(t)
	if _, ok := routed[user.UserId()]; !ok || len(routed) != 1 {
		t.Errorf("routed = %v, want only the user's own 'me'", routed)
	}
}
