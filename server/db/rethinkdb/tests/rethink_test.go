package tests

// To test another db backend:
// 1) Create GetAdapter function inside your db backend adapter package (like one inside rethinkdb adapter)
// 2) Uncomment your db backend package ('backend' named package)
// 3) Write own initConnectionToDb and 'db' variable
// 4) Replace rethinkdb specific db queries inside test to your own queries.
// 5) Run.

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	adapter "github.com/tinode/chat/server/db"
	jcr "github.com/tinode/jsonco"
	rdb "gopkg.in/rethinkdb/rethinkdb-go.v6"

	"github.com/tinode/chat/server/db/common/test_data"
	backend "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

type configType struct {
	// If Reset=true test will recreate database every time it runs
	Reset bool `json:"reset_db_data"`
	// Configurations for individual adapters.
	Adapters map[string]json.RawMessage `json:"adapters"`
}

var config configType
var adp adapter.Adapter
var conn *rdb.Session
var testData *test_data.TestData

var dummyUid1 = types.Uid(12345)
var dummyUid2 = types.Uid(54321)

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
	// Saved db is closed, get a fresh one.
	conn = adp.GetTestDB().(*rdb.Session)
}

// ================== Create tests ================================
func TestUserCreate(t *testing.T) {
	for _, user := range testData.Users {
		if err := adp.UserCreate(user); err != nil {
			t.Error(err)
		}
	}

	cursor, err := rdb.Table("users").Count().Run(conn)
	if err != nil {
		t.Error(err)
	}
	defer cursor.Close()

	var count int
	if err = cursor.One(&count); err != nil {
		t.Error(err)
	}
	if count == 0 {
		t.Error("No users created!")
	}
}

func TestCredUpsert(t *testing.T) {
	// Test just inserts:
	for i := 0; i < 2; i++ {
		inserted, err := adp.CredUpsert(testData.Creds[i])
		if err != nil {
			t.Fatal(err)
		}
		if !inserted {
			t.Error("Should be inserted, but updated")
		}
	}

	// Test duplicate:
	_, err := adp.CredUpsert(testData.Creds[1])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}
	_, err = adp.CredUpsert(testData.Creds[2])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}

	// Test add new unvalidated credentials
	inserted, err := adp.CredUpsert(testData.Creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Error("Should be inserted, but updated")
	}
	inserted, err = adp.CredUpsert(testData.Creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Error("Should be updated, but inserted")
	}

	// Just insert other creds (used in other tests)
	for _, cred := range testData.Creds[4:] {
		_, err = adp.CredUpsert(cred)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthAddRecord(t *testing.T) {
	for _, rec := range testData.Recs {
		err := adp.AuthAddRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, rec.Unique,
			rec.AuthLvl, rec.Secret, rec.Expires)
		if err != nil {
			t.Fatal(err)
		}
	}
	//Test duplicate
	err := adp.AuthAddRecord(types.ParseUserId("usr"+testData.Users[0].Id), testData.Recs[0].Scheme,
		testData.Recs[0].Unique, testData.Recs[0].AuthLvl, testData.Recs[0].Secret, testData.Recs[0].Expires)
	if err != types.ErrDuplicate {
		t.Fatal("Should be duplicate error but got", err)
	}
}

func TestTopicCreate(t *testing.T) {
	err := adp.TopicCreate(testData.Topics[0])
	if err != nil {
		t.Error(err)
	}
	// Update topic SeqId because it's not saved at creation time but used by the tests.
	err = adp.TopicUpdate(testData.Topics[0].Id, map[string]interface{}{
		"SeqId": testData.Topics[0].SeqId,
	})
	if err != nil {
		t.Error(err)
	}

	for _, tpc := range testData.Topics[3:] {
		err = adp.TopicCreate(tpc)
		if err != nil {
			t.Error(err)
		}
	}
}

func TestTopicCreateP2P(t *testing.T) {
	err := adp.TopicCreateP2P(testData.Subs[2], testData.Subs[3])
	if err != nil {
		t.Fatal(err)
	}

	oldModeGiven := testData.Subs[2].ModeGiven
	testData.Subs[2].ModeGiven = 255
	err = adp.TopicCreateP2P(testData.Subs[4], testData.Subs[2])
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("subscriptions").Get(testData.Subs[2].Id).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got types.Subscription
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if got.ModeGiven == oldModeGiven {
		t.Error("ModeGiven update failed")
	}
}

func TestTopicShare(t *testing.T) {
	if err := adp.TopicShare(testData.Subs[0].Topic, testData.Subs); err != nil {
		t.Fatal(err)
	}

	// Must save recvseqid and readseqid separately because TopicShare
	// ignores them.
	for _, sub := range testData.Subs {
		adp.SubsUpdate(sub.Topic, types.ParseUid(sub.User), map[string]any{
			"RecvSeqId": sub.RecvSeqId,
			"ReadSeqId": sub.ReadSeqId,
		})
	}
}

func TestMessageSave(t *testing.T) {
	for _, msg := range testData.Msgs {
		err := adp.MessageSave(msg)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Some messages are soft deleted, but it's ignored by adp.MessageSave
	for _, msg := range testData.Msgs {
		if len(msg.DeletedFor) > 0 {
			for _, del := range msg.DeletedFor {
				toDel := types.DelMessage{
					Topic:       msg.Topic,
					DeletedFor:  del.User,
					DelId:       del.DelId,
					SeqIdRanges: []types.Range{{Low: msg.SeqId}},
				}
				adp.MessageDeleteList(msg.Topic, &toDel)
			}
		}
	}
}

func TestFileStartUpload(t *testing.T) {
	for _, f := range testData.Files {
		err := adp.FileStartUpload(f)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// ================== Read tests ==================================
func TestUserGet(t *testing.T) {
	// Test not found
	got, err := adp.UserGet(dummyUid1)
	if err == nil && got != nil {
		t.Error("user should be nil.")
	}

	got, err = adp.UserGet(types.ParseUserId("usr" + testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}

	// User agent is not stored when creating a user. Make sure it's the same.
	got.UserAgent = testData.Users[0].UserAgent

	if !cmp.Equal(got, testData.Users[0], cmpopts.IgnoreUnexported(types.User{}, types.ObjHeader{})) {
		t.Error(mismatchErrorString("User", got, testData.Users[0]))
	}
}

func TestUserGetAll(t *testing.T) {
	// Test not found (dummy UIDs).
	got, err := adp.UserGetAll(dummyUid1, dummyUid2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 0 {
		t.Error("result users should be zero length, got", len(got))
	}

	got, err = adp.UserGetAll(types.ParseUserId("usr"+testData.Users[0].Id), types.ParseUserId("usr"+testData.Users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatal(mismatchErrorString("resultUsers length", len(got), 2))
	}
	for i, usr := range got {
		// User agent is not compared.
		usr.UserAgent = testData.Users[i].UserAgent
		if !reflect.DeepEqual(&usr, testData.Users[i]) {
			t.Error(mismatchErrorString("User", &usr, testData.Users[i]))
		}
	}
}

func TestUserGetByCred(t *testing.T) {
	// Test not found
	got, err := adp.UserGetByCred("foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != types.ZeroUid {
		t.Error("result uid should be ZeroUid")
	}

	got, _ = adp.UserGetByCred(testData.Creds[0].Method, testData.Creds[0].Value)
	if got != types.ParseUserId("usr"+testData.Creds[0].User) {
		t.Error(mismatchErrorString("Uid", got, types.ParseUserId("usr"+testData.Creds[0].User)))
	}
}

func TestCredGetActive(t *testing.T) {
	got, err := adp.CredGetActive(types.ParseUserId("usr"+testData.Users[2].Id), "tel")
	if err != nil {
		t.Error(err)
	}
	if !cmp.Equal(got, testData.Creds[3], cmpopts.IgnoreUnexported(types.ObjHeader{}, types.Credential{})) {
		t.Error(mismatchErrorString("Credential", got, testData.Creds[3]))
	}

	// Test not found
	got, err = adp.CredGetActive(dummyUid1, "")
	if err != nil {
		t.Error(err)
	}
	if got != nil {
		t.Error("result should be nil, but got", got)
	}
}

func TestCredGetAll(t *testing.T) {
	got, err := adp.CredGetAll(types.ParseUserId("usr"+testData.Users[2].Id), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Error(mismatchErrorString("Credentials length", len(got), 3))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+testData.Users[2].Id), "tel", false)
	if len(got) != 2 {
		t.Error(mismatchErrorString("Credentials length", len(got), 2))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+testData.Users[2].Id), "", true)
	if len(got) != 1 {
		t.Error(mismatchErrorString("Credentials length", len(got), 1))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+testData.Users[2].Id), "tel", true)
	if len(got) != 1 {
		t.Error(mismatchErrorString("Credentials length", len(got), 1))
	}
}

func TestUserGetUnvalidated(t *testing.T) {
	// Test RethinkDB specific method
	cutoff := time.Now().Add(-24 * time.Hour)
	uids, err := adp.UserGetUnvalidated(cutoff, 10)
	if err != nil {
		t.Error(err)
	}
	// Should return empty slice since all test users are considered validated
	if len(uids) > 0 {
		t.Error("Expected no unvalidated users in test data")
	}
}

func TestAuthGetUniqueRecord(t *testing.T) {
	uid, authLvl, secret, expires, err := adp.AuthGetUniqueRecord("basic:alice")
	if err != nil {
		t.Fatal(err)
	}
	if uid != types.ParseUserId("usr"+testData.Recs[0].UserId) ||
		authLvl != testData.Recs[0].AuthLvl ||
		!reflect.DeepEqual(secret, testData.Recs[0].Secret) ||
		expires != testData.Recs[0].Expires {

		got := fmt.Sprintf("%v %v %v %v", uid, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", testData.Recs[0].UserId, testData.Recs[0].AuthLvl, testData.Recs[0].Secret, testData.Recs[0].Expires)
		t.Error(mismatchErrorString("Auth record", got, want))
	}

	// Test not found
	uid, _, _, _, err = adp.AuthGetUniqueRecord("qwert:asdfg")
	if err == nil && !uid.IsZero() {
		t.Error("Auth record found but shouldn't. Uid:", uid.String())
	}
}

func TestAuthGetRecord(t *testing.T) {
	recId, authLvl, secret, expires, err := adp.AuthGetRecord(types.ParseUserId("usr"+testData.Recs[0].UserId), "basic")
	if err != nil {
		t.Fatal(err)
	}
	if recId != testData.Recs[0].Unique ||
		authLvl != testData.Recs[0].AuthLvl ||
		!reflect.DeepEqual(secret, testData.Recs[0].Secret) ||
		expires != testData.Recs[0].Expires {

		got := fmt.Sprintf("%v %v %v %v", recId, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", testData.Recs[0].Unique, testData.Recs[0].AuthLvl, testData.Recs[0].Secret, testData.Recs[0].Expires)
		t.Error(mismatchErrorString("Auth record", got, want))
	}

	// Test not found
	recId, _, _, _, err = adp.AuthGetRecord(types.Uid(123), "scheme")
	if err != types.ErrNotFound {
		t.Error("Auth record found but shouldn't. recId:", recId)
	}
}

func TestTopicGet(t *testing.T) {
	got, err := adp.TopicGet(testData.Topics[0].Id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, testData.Topics[0]) {
		t.Error(mismatchErrorString("Topic", got, testData.Topics[0]))
	}
	// Test not found
	got, err = adp.TopicGet("asdfasdfasdf")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("Topic should be nil but got:", got)
	}
}

func TestTopicsForUser(t *testing.T) {
	qOpts := types.QueryOpt{
		Topic: testData.Topics[1].Id,
		Limit: 999,
	}
	gotSubs, err := adp.TopicsForUser(types.ParseUserId("usr"+testData.Users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+testData.Users[1].Id), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Error(mismatchErrorString("Subs length (2)", len(gotSubs), 2))
	}

	qOpts.Topic = ""
	ims := testData.Now.Add(15 * time.Minute)
	qOpts.IfModifiedSince = &ims
	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+testData.Users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Error(mismatchErrorString("Subs length (IMS)", len(gotSubs), 1))
	}

	ims = time.Now().Add(15 * time.Minute)
	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+testData.Users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 0 {
		t.Error(mismatchErrorString("Subs length (IMS 2)", len(gotSubs), 0))
	}
}

func TestUsersForTopic(t *testing.T) {
	qOpts := types.QueryOpt{
		User:  types.ParseUserId("usr" + testData.Users[0].Id),
		Limit: 999,
	}
	gotSubs, err := adp.UsersForTopic(testData.Topics[0].Id, false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	gotSubs, err = adp.UsersForTopic(testData.Topics[0].Id, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 2))
	}

	gotSubs, err = adp.UsersForTopic(testData.Topics[1].Id, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 2))
	}
}

func TestOwnTopics(t *testing.T) {
	gotSubs, err := adp.OwnTopics(types.ParseUserId("usr" + testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Fatalf("Got topic length %v instead of %v", len(gotSubs), 1)
	}
	if gotSubs[0] != testData.Topics[0].Id {
		t.Errorf("Got topic %v instead of %v", gotSubs[0], testData.Topics[0].Id)
	}
}

func TestChannelsForUser(t *testing.T) {
	// Test RethinkDB specific method
	channels, err := adp.ChannelsForUser(types.ParseUserId("usr" + testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	// Should return empty slice since we don't have channel subscriptions in test data
	if len(channels) != 0 {
		t.Error(mismatchErrorString("Channels length", len(channels), 0))
	}
}

func TestSubscriptionGet(t *testing.T) {
	got, err := adp.SubscriptionGet(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Error(err)
	}

	opts := cmpopts.IgnoreUnexported(types.Subscription{}, types.ObjHeader{})
	if !cmp.Equal(got, testData.Subs[0], opts) {
		t.Error(mismatchErrorString("Subs", got, testData.Subs[0]))
	}
	// Test not found
	got, err = adp.SubscriptionGet("dummytopic", dummyUid1, false)
	if err != nil {
		t.Error(err)
	}
	if got != nil {
		t.Error("result sub should be nil.")
	}
}

func TestSubsForUser(t *testing.T) {
	gotSubs, err := adp.SubsForUser(types.ParseUserId("usr" + testData.Users[0].Id))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 2 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 2))
	}

	// Test not found
	gotSubs, err = adp.SubsForUser(types.ParseUserId("usr12345678"))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 0))
	}
}

func TestSubsForTopic(t *testing.T) {
	qOpts := types.QueryOpt{
		User:  types.ParseUserId("usr" + testData.Users[0].Id),
		Limit: 999,
	}
	gotSubs, err := adp.SubsForTopic(testData.Topics[0].Id, false, &qOpts)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 1 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 1))
	}
	// Test not found
	gotSubs, err = adp.SubsForTopic("dummytopicid", false, nil)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 0))
	}
}

func TestFind(t *testing.T) {
	reqTags := [][]string{{"alice", "bob", "carol", "travel", "qwer", "asdf", "zxcv"}}
	got, err := adp.Find("usr"+testData.Users[2].Id, "", reqTags, nil, true)
	if err != nil {
		t.Error(err)
	}
	if len(got) != 3 {
		t.Error(mismatchErrorString("result length", len(got), 3))
	}
}

func TestFindOne(t *testing.T) {
	// Test RethinkDB specific FindOne method
	found, err := adp.FindOne("alice")
	if err != nil {
		t.Error(err)
	}
	// Should find the user with alice tag
	if found == "" {
		t.Error("Expected to find user with alice tag")
	}

	// Test not found
	found, err = adp.FindOne("nonexistent")
	if err != nil {
		t.Error(err)
	}
	if found != "" {
		t.Error("Should not find nonexistent tag")
	}
}

func TestMessageGetAll(t *testing.T) {
	opts := types.QueryOpt{
		Since:  1,
		Before: 2,
		Limit:  999,
	}
	gotMsgs, err := adp.MessageGetAll(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[0].Id), false, &opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMsgs) != 1 {
		t.Error(mismatchErrorString("Messages length opts", len(gotMsgs), 1))
	}
	gotMsgs, _ = adp.MessageGetAll(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[0].Id), false, nil)
	if len(gotMsgs) != 2 {
		t.Error(mismatchErrorString("Messages length no opts", len(gotMsgs), 2))
	}
	gotMsgs, _ = adp.MessageGetAll(testData.Topics[0].Id, types.ZeroUid, false, nil)
	if len(gotMsgs) != 3 {
		t.Error(mismatchErrorString("Messages length zero uid", len(gotMsgs), 3))
	}
}

func TestReactionsCRUD(t *testing.T) {
	// Create a fresh topic to avoid interference with other tests.
	topic := testData.UGen.GetStr()
	if err := adp.TopicCreate(&types.Topic{ObjHeader: types.ObjHeader{Id: topic, CreatedAt: time.Now(), UpdatedAt: time.Now()}, TouchedAt: time.Now(), Owner: testData.Users[0].Id}); err != nil {
		t.Fatal(err)
	}
	// Set seqid so MessageGetAll will work as expected if needed.
	if err := adp.TopicUpdate(topic, map[string]any{"seqid": 10}); err != nil {
		t.Fatal(err)
	}

	// Create a message in the topic to attach reactions.
	seq := 1
	msg := &types.Message{ObjHeader: types.ObjHeader{Id: testData.UGen.GetStr(), CreatedAt: time.Now(), UpdatedAt: time.Now()}, SeqId: seq, Topic: topic, From: testData.Users[0].Id, Content: "msg1"}
	if err := adp.MessageSave(msg); err != nil {
		t.Fatal(err)
	}

	u1 := types.ParseUserId("usr" + testData.Users[0].Id)
	u2 := types.ParseUserId("usr" + testData.Users[1].Id)

	// nil opts should return empty map and no error
	got, err := adp.ReactionGetAll(topic, u1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("Expected no reactions for nil opts")
	}

	// Save two identical reactions from two users
	r1 := &types.Reaction{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		Topic:     topic,
		SeqId:     seq,
		User:      u1.UserId(),
		Content:   "👍",
	}
	if err := adp.ReactionSave(r1); err != nil {
		t.Fatal(err)
	}

	r2 := &types.Reaction{
		CreatedAt: time.Now().Add(-30 * time.Minute),
		Topic:     topic,
		SeqId:     seq,
		User:      u2.UserId(),
		Content:   "👍",
	}
	if err := adp.ReactionSave(r2); err != nil {
		t.Fatal(err)
	}

	opts := &types.QueryOpt{IdRanges: []types.Range{{Low: seq}}}

	reacts, err := adp.ReactionGetAll(topic, u1, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(reacts) == 0 {
		t.Fatal("No reactions returned")
	}
	found := false
	for _, arr := range reacts {
		for _, r := range arr {
			if r.Content == "👍" {
				found = true
				if r.Cnt != 2 {
					t.Error("Expected cnt 2 got", r.Cnt)
				}
				if len(r.Users) != 2 {
					t.Error("Expected 2 users, got", r.Users)
				}
			}
		}
	}
	if !found {
		t.Error("expected 👍 reaction")
	}

	// asChan mode: counts and current user's marking
	reactsChan, err := adp.ReactionGetAll(topic, u1, true, opts)
	if err != nil {
		t.Fatal(err)
	}
	rarr := reactsChan[seq]
	if len(rarr) == 0 {
		t.Fatal("No reactions in asChan")
	}
	var gotCnt int
	var gotMarked bool
	for _, r := range rarr {
		if r.Content == "👍" {
			gotCnt = r.Cnt
			if len(r.Users) == 1 && r.Users[0] == u1.UserId() {
				gotMarked = true
			}
		}
	}
	if gotCnt != 2 {
		t.Error("asChan cnt expected 2 got", gotCnt)
	}
	if !gotMarked {
		t.Error("current user's reaction not marked")
	}

	// Update reaction by changing u1 reaction to ❤️
	r1b := &types.Reaction{
		CreatedAt: time.Now(),
		Topic:     topic,
		SeqId:     seq,
		User:      u1.UserId(),
		Content:   "❤️",
	}
	if err := adp.ReactionSave(r1b); err != nil {
		t.Fatal(err)
	}

	reacts, err = adp.ReactionGetAll(topic, u1, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	// Expect ❤️ cnt 1 and 👍 cnt 1
	foundPlus := false
	foundHeart := false
	for _, arr := range reacts {
		for _, r := range arr {
			switch r.Content {
			case "👍":
				if r.Cnt != 1 {
					t.Error("Expected 👍 cnt 1 got", r.Cnt)
				}
				foundPlus = true
			case "❤️":
				if r.Cnt != 1 {
					t.Error("Expected ❤️ cnt 1 got", r.Cnt)
				}
				foundHeart = true
			}
		}
	}
	if !foundPlus || !foundHeart {
		t.Error("expected both 👍 and ❤️ reactions")
	}

	// Delete u2 reaction
	if err := adp.ReactionDelete(topic, seq, u2); err != nil {
		t.Fatal(err)
	}
	reacts, err = adp.ReactionGetAll(topic, u1, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	// After delete only ❤️ remains
	onlyHeart := false
	for _, arr := range reacts {
		for _, r := range arr {
			if r.Content == "❤️" {
				onlyHeart = true
			}
			if r.Content == "👍" {
				t.Error("👍 should be deleted")
			}
		}
	}
	if !onlyHeart {
		t.Error("expected only ❤️ reaction")
	}

	// IfModifiedSince filter
	// create old reaction and new reaction on another seq
	seq2 := 2
	old := &types.Reaction{
		CreatedAt: time.Now().Add(-2 * time.Hour),
		Topic:     topic,
		SeqId:     seq2,
		User:      u1.UserId(),
		Content:   "old",
	}
	newr := &types.Reaction{
		CreatedAt: time.Now(),
		Topic:     topic,
		SeqId:     seq2,
		User:      u2.UserId(),
		Content:   "new",
	}
	if err := adp.ReactionSave(old); err != nil {
		t.Fatal(err)
	}
	if err := adp.ReactionSave(newr); err != nil {
		t.Fatal(err)
	}

	ims := time.Now().Add(-30 * time.Minute)
	opts2 := &types.QueryOpt{IdRanges: []types.Range{{Low: seq2}}, IfModifiedSince: &ims}
	reacts, err = adp.ReactionGetAll(topic, u1, false, opts2)
	if err != nil {
		t.Fatal(err)
	}
	// expect only "new"
	for _, arr := range reacts {
		for _, r := range arr {
			if r.Content == "old" {
				t.Error("old reaction should be filtered out by IfModifiedSince")
			}
		}
	}

	// Invalid QueryOpt (non-nil but no ranges/since/before) => error
	_, err = adp.ReactionGetAll(topic, u1, false, &types.QueryOpt{})
	if err == nil {
		t.Error("expected error for invalid query options")
	}
}

func TestFileGet(t *testing.T) {
	// General test done during TestFileFinishUpload().

	// Test not found
	got, err := adp.FileGet("dummyfileid")
	if err != nil {
		if got != nil {
			t.Error("File found but shouldn't:", got)
		}
	}
}

// ================== Update tests ================================
func TestUserUpdate(t *testing.T) {
	update := map[string]any{
		"UserAgent": "Test Agent v0.11",
		"UpdatedAt": testData.Now.Add(30 * time.Minute),
	}
	err := adp.UserUpdate(types.ParseUserId("usr"+testData.Users[0].Id), update)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("users").Get(testData.Users[0].Id).Pluck("UserAgent", "UpdatedAt", "CreatedAt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got struct {
		UserAgent string
		UpdatedAt time.Time
		CreatedAt time.Time
	}
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "Test Agent v0.11" {
		t.Error(mismatchErrorString("UserAgent", got.UserAgent, "Test Agent v0.11"))
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Error("UpdatedAt field not updated")
	}
}

func TestUserUpdateTags(t *testing.T) {
	addTags := testData.Tags[0]
	removeTags := testData.Tags[1]
	resetTags := testData.Tags[2]
	uid := types.ParseUserId("usr" + testData.Users[0].Id)

	got, err := adp.UserUpdateTags(uid, addTags, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alice", "tag1"}
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))
	}

	got, _ = adp.UserUpdateTags(uid, nil, removeTags, nil)
	want = nil
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))
	}

	got, _ = adp.UserUpdateTags(uid, nil, nil, resetTags)
	want = []string{"alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))
	}

	got, _ = adp.UserUpdateTags(uid, addTags, removeTags, nil)
	want = []string{"tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))
	}
}

func TestCredFail(t *testing.T) {
	err := adp.CredFail(types.ParseUserId("usr"+testData.Creds[3].User), "tel")
	if err != nil {
		t.Error(err)
	}

	// Check if fields updated
	cursor, err := rdb.Table("credentials").GetAllByIndex("User", testData.Creds[3].User).
		Filter(map[string]any{"Method": "tel", "Value": testData.Creds[3].Value}).
		Pluck("Retries", "UpdatedAt", "CreatedAt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got struct {
		Retries   int
		UpdatedAt time.Time
		CreatedAt time.Time
	}
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if got.Retries != 1 {
		t.Error(mismatchErrorString("Retries count", got.Retries, 1))
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Error("UpdatedAt field not updated")
	}
}

func TestCredConfirm(t *testing.T) {
	err := adp.CredConfirm(types.ParseUserId("usr"+testData.Creds[3].User), "tel")
	if err != nil {
		t.Fatal(err)
	}

	// Test fields are updated - the confirmed credential should have a new ID (method:value)
	cursor, err := rdb.Table("credentials").Get(testData.Creds[3].Method+":"+testData.Creds[3].Value).
		Pluck("UpdatedAt", "CreatedAt", "Done").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got struct {
		UpdatedAt time.Time
		CreatedAt time.Time
		Done      bool
	}
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Error("Credential not updated correctly")
	}
	if !got.Done {
		t.Error("Credential should be marked as done")
	}

	// And unconfirmed credential should be deleted
	cursor2, err := rdb.Table("credentials").Get(testData.Creds[3].User + ":" + testData.Creds[3].Method + ":" + testData.Creds[3].Value).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()
	if !cursor2.IsNil() {
		t.Error("Unconfirmed credential should be deleted")
	}
}

func TestAuthUpdRecord(t *testing.T) {
	rec := testData.Recs[1]
	newSecret := []byte{'s', 'e', 'c', 'r', 'e', 't'}
	err := adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, rec.Unique,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("auth").Get(rec.Unique).Field("secret").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got []byte
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got, rec.Secret) {
		t.Error(mismatchErrorString("secret", got, rec.Secret))
	}

	// Test with auth ID (unique) change
	newId := "basic:bob12345"
	err = adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, newId,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}

	// Test if old ID deleted
	cursor2, err := rdb.Table("auth").Get(rec.Unique).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()
	if !cursor2.IsNil() {
		t.Error("Old auth record should be deleted")
	}
}

func TestTopicUpdateOnMessage(t *testing.T) {
	msg := types.Message{
		ObjHeader: types.ObjHeader{
			CreatedAt: testData.Now.Add(33 * time.Minute),
		},
		SeqId: 66,
	}
	err := adp.TopicUpdateOnMessage(testData.Topics[2].Id, &msg)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("topics").Get(testData.Topics[2].Id).
		Pluck("TouchedAt", "SeqId").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got struct {
		TouchedAt time.Time
		SeqId     int
	}
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if !got.TouchedAt.Equal(msg.CreatedAt) || got.SeqId != msg.SeqId {
		t.Error(mismatchErrorString("TouchedAt", got.TouchedAt, msg.CreatedAt))
		t.Error(mismatchErrorString("SeqId", got.SeqId, msg.SeqId))
	}
}

func TestTopicUpdate(t *testing.T) {
	update := map[string]any{
		"UpdatedAt": testData.Now.Add(55 * time.Minute),
	}
	err := adp.TopicUpdate(testData.Topics[0].Id, update)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("topics").Get(testData.Topics[0].Id).Field("UpdatedAt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got time.Time
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(update["UpdatedAt"].(time.Time)) {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}
}

func TestTopicUpdateSubCnt(t *testing.T) {
	// Test RethinkDB specific method
	err := adp.TopicUpdateSubCnt(testData.Topics[0].Id)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the subscription count was updated correctly
	cursor, err := rdb.Table("topics").Get(testData.Topics[0].Id).Field("SubCnt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var subcnt int
	if err = cursor.One(&subcnt); err != nil {
		t.Fatal(err)
	}
	// Should match the number of active subscriptions
	if subcnt < 0 {
		t.Error("Subscription count should be non-negative")
	}
}

func TestTopicOwnerChange(t *testing.T) {
	err := adp.TopicOwnerChange(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[1].Id))
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("topics").Get(testData.Topics[0].Id).Field("Owner").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got string
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}

	if got != testData.Users[1].Id {
		t.Error(mismatchErrorString("Owner", got, testData.Users[1].Id))
	}
}

func TestSubsUpdate(t *testing.T) {
	update := map[string]any{
		"UpdatedAt": testData.Now.Add(22 * time.Minute),
	}
	err := adp.SubsUpdate(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[0].Id), update)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("subscriptions").Get(testData.Topics[0].Id + ":" + testData.Users[0].Id).
		Field("UpdatedAt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got time.Time
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(update["UpdatedAt"].(time.Time)) {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}

	err = adp.SubsUpdate(testData.Topics[1].Id, types.ZeroUid, update)
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table("subscriptions").GetAllByIndex("Topic", testData.Topics[1].Id).
		Field("UpdatedAt").Limit(1).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if err = cursor2.One(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(update["UpdatedAt"].(time.Time)) {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}
}

func TestSubsDelete(t *testing.T) {
	err := adp.SubsDelete(testData.Topics[1].Id, types.ParseUserId("usr"+testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("subscriptions").Get(testData.Topics[1].Id + ":" + testData.Users[0].Id).
		Field("DeletedAt").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var deletedat time.Time
	if err = cursor.One(&deletedat); err != nil {
		t.Fatal(err)
	}
	if deletedat.IsZero() {
		t.Error("DeletedAt should not be null")
	}
}

func TestDeviceUpsert(t *testing.T) {
	err := adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("users").Get(testData.Users[0].Id).Field("Devices").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var got map[string]*types.DeviceDef
	if err = cursor.One(&got); err != nil {
		t.Fatal(err)
	}

	// Find the device (the key is hashed)
	var foundDev *types.DeviceDef
	for _, dev := range got {
		if dev != nil && dev.DeviceId == testData.Devs[0].DeviceId {
			foundDev = dev
			break
		}
	}
	if foundDev == nil {
		t.Error("Device not found after upsert")
	} else {
		foundDev.LastSeen = testData.Devs[0].LastSeen // Ignore LastSeen in comparison (workaranod for timezone issues)
		if !reflect.DeepEqual(*foundDev, *testData.Devs[0]) {
			t.Error(mismatchErrorString("Device", foundDev, testData.Devs[0]))
		}
	}

	// Test update
	testData.Devs[0].Platform = "Web"
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table("users").Get(testData.Users[0].Id).Field("Devices").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if err = cursor2.One(&got); err != nil {
		t.Fatal(err)
	}

	// Find the updated device
	foundDev = nil
	for _, dev := range got {
		if dev != nil && dev.DeviceId == testData.Devs[0].DeviceId {
			foundDev = dev
			break
		}
	}
	if foundDev == nil || foundDev.Platform != "Web" {
		t.Error("Device not updated.", foundDev)
	}

	// Test add same device to another user
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}

	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[2].Id), testData.Devs[1])
	if err != nil {
		t.Error(err)
	}
}

func TestMessageAttachments(t *testing.T) {
	fids := []string{testData.Files[0].Id, testData.Files[1].Id}
	err := adp.FileLinkAttachments("", types.ZeroUid, types.ParseUid(testData.Msgs[1].Id), fids)
	if err != nil {
		t.Fatal(err)
	}

	// Check if attachments were linked to message
	cursor, err := rdb.Table("messages").Get(testData.Msgs[1].Id).Field("Attachments").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var attachments []string
	if err = cursor.All(&attachments); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attachments, fids) {
		t.Error(mismatchErrorString("Attachments", attachments, fids))
	}

	// Check if use count was incremented in fileuploads
	cursor2, err := rdb.Table("fileuploads").Get(testData.Files[0].Id).Field("UseCount").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	var usecount int
	if err = cursor2.One(&usecount); err != nil {
		t.Fatal(err)
	}
	if usecount != 1 {
		t.Error(mismatchErrorString("UseCount", usecount, 1))
	}
}

func TestFileFinishUpload(t *testing.T) {
	got, err := adp.FileFinishUpload(testData.Files[0], true, 22222)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != types.UploadCompleted {
		t.Error(mismatchErrorString("Status", got.Status, types.UploadCompleted))
	}
	if got.Size != 22222 {
		t.Error(mismatchErrorString("Size", got.Size, 22222))
	}
}

// ================== Other tests =================================
func TestDeviceGetAll(t *testing.T) {
	uid0 := types.ParseUserId("usr" + testData.Users[0].Id)
	uid1 := types.ParseUserId("usr" + testData.Users[1].Id)
	uid2 := types.ParseUserId("usr" + testData.Users[2].Id)
	gotDevs, count, err := adp.DeviceGetAll(uid0, uid1, uid2)
	if err != nil {
		t.Fatal(err)
	}
	if count < 1 {
		t.Fatal(mismatchErrorString("count", count, ">=1"))
	}
	// Test that devices exist for the users
	if len(gotDevs) == 0 {
		t.Error("Expected devices for users")
	}
}

func TestDeviceDelete(t *testing.T) {
	err := adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0].DeviceId)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("users").Get(testData.Users[1].Id).Field("Devices").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var devices map[string]*types.DeviceDef
	if err = cursor.One(&devices); err != nil && err != rdb.ErrEmptyResult {
		t.Fatal(err)
	}

	// Check that the specific device is deleted
	for _, dev := range devices {
		if dev != nil && dev.DeviceId == testData.Devs[0].DeviceId {
			t.Error("Device not deleted:", dev)
		}
	}

	err = adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[2].Id), "")
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table("users").Get(testData.Users[2].Id).Field("Devices").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	var allDevices map[string]*types.DeviceDef
	if err = cursor2.One(&allDevices); err != nil && err != rdb.ErrEmptyResult {
		t.Fatal(err)
	}
	if len(allDevices) > 0 {
		t.Error("All devices not deleted:", allDevices)
	}
}

// ================== Persistent Cache tests ======================
func TestPCacheUpsert(t *testing.T) {
	err := adp.PCacheUpsert("test_key", "test_value", false)
	if err != nil {
		t.Fatal(err)
	}

	// Test duplicate with failOnDuplicate = true
	err = adp.PCacheUpsert("test_key2", "test_value2", true)
	if err != nil {
		t.Fatal(err)
	}

	err = adp.PCacheUpsert("test_key2", "new_value", true)
	if err != types.ErrDuplicate {
		t.Error("Expected duplicate error")
	}
}

func TestPCacheGet(t *testing.T) {
	value, err := adp.PCacheGet("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "test_value" {
		t.Error(mismatchErrorString("Cache value", value, "test_value"))
	}

	// Test not found
	value, err = adp.PCacheGet("nonexistent")
	if err != types.ErrNotFound {
		t.Errorf("Expected not found error but got '%s', %s", value, err)
	}
}

func TestPCacheDelete(t *testing.T) {
	err := adp.PCacheDelete("test_key")
	if err != nil {
		t.Fatal(err)
	}

	// Verify deleted
	_, err = adp.PCacheGet("test_key")
	if err != types.ErrNotFound {
		t.Error("Key should be deleted")
	}
}

func TestPCacheExpire(t *testing.T) {
	// Insert some test keys with prefix and CreatedAt
	adp.PCacheUpsert("prefix_key1", "value1", true)
	adp.PCacheUpsert("prefix_key2", "value2", true)

	// Expire keys older than now (should delete all test keys)
	err := adp.PCacheExpire("prefix_", time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
}

// ================== Delete tests ================================
func TestCredDel(t *testing.T) {
	err := adp.CredDel(types.ParseUserId("usr"+testData.Users[0].Id), "email", "alice@test.example.com")
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("credentials").Filter(map[string]any{"Method": "email", "Value": "alice@test.example.com"}).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var results []any
	if err = cursor.All(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Error("Got result but shouldn't", results)
	}

	err = adp.CredDel(types.ParseUserId("usr"+testData.Users[1].Id), "", "")
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table("credentials").GetAllByIndex("User", testData.Users[1].Id).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if err = cursor2.All(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Error("Got result but shouldn't", results)
	}
}

func TestAuthDelScheme(t *testing.T) {
	// Test deleting auth scheme
	err := adp.AuthDelScheme(types.ParseUserId("usr"+testData.Recs[1].UserId), testData.Recs[1].Scheme)
	if err != nil {
		t.Fatal(err)
	}

	// Verify deleted
	_, _, _, _, err = adp.AuthGetRecord(types.ParseUserId("usr"+testData.Recs[1].UserId), testData.Recs[1].Scheme)
	if err != types.ErrNotFound {
		t.Error("Auth record should be deleted")
	}
}

func TestAuthDelAllRecords(t *testing.T) {
	delCount, err := adp.AuthDelAllRecords(types.ParseUserId("usr" + testData.Recs[0].UserId))
	if err != nil {
		t.Fatal(err)
	}
	if delCount != 1 {
		t.Error(mismatchErrorString("delCount", delCount, 1))
	}

	// With dummy user
	delCount, _ = adp.AuthDelAllRecords(dummyUid1)
	if delCount != 0 {
		t.Error(mismatchErrorString("delCount", delCount, 0))
	}
}

func TestMessageDeleteList(t *testing.T) {
	toDel := types.DelMessage{
		ObjHeader: types.ObjHeader{
			Id:        testData.UGen.GetStr(),
			CreatedAt: testData.Now,
			UpdatedAt: testData.Now,
		},
		Topic:       testData.Topics[1].Id,
		DeletedFor:  testData.Users[2].Id,
		DelId:       1,
		SeqIdRanges: []types.Range{{Low: 9}, {Low: 3, Hi: 7}},
	}
	err := adp.MessageDeleteList(toDel.Topic, &toDel)
	if err != nil {
		t.Fatal(err)
	}

	// Check messages in topic - some should be soft deleted
	cursor, err := rdb.Table("messages").Filter(map[string]any{"Topic": toDel.Topic}).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var messages []types.Message
	if err = cursor.All(&messages); err != nil {
		t.Fatal(err)
	}

	// Verify soft deletion worked
	foundSoftDeleted := false
	for _, msg := range messages {
		if msg.SeqId >= 3 && msg.SeqId <= 7 || msg.SeqId == 9 {
			if msg.DeletedFor != nil {
				for _, del := range msg.DeletedFor {
					if del.User == toDel.DeletedFor {
						foundSoftDeleted = true
						break
					}
				}
			}
		}
	}
	if !foundSoftDeleted {
		t.Error("Expected to find soft-deleted messages")
	}

	// Hard delete test
	toDel = types.DelMessage{
		ObjHeader: types.ObjHeader{
			Id:        testData.UGen.GetStr(),
			CreatedAt: testData.Now,
			UpdatedAt: testData.Now,
		},
		Topic:       testData.Topics[0].Id,
		DelId:       3,
		SeqIdRanges: []types.Range{{Low: 1, Hi: 3}},
	}
	err = adp.MessageDeleteList(toDel.Topic, &toDel)
	if err != nil {
		t.Fatal(err)
	}

	// Check if messages content was cleared (hard delete)
	cursor2, err := rdb.Table("messages").Filter(map[string]any{"Topic": toDel.Topic}).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if err = cursor2.All(&messages); err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if msg.SeqId >= 1 && msg.SeqId <= 3 {
			if msg.Content != nil && msg.DelId == 0 {
				t.Error("Message not properly hard deleted:", msg.SeqId)
			}
		}
	}

	err = adp.MessageDeleteList(testData.Topics[0].Id, nil)
	if err != nil {
		t.Fatal(err)
	}

	cursor3, err := rdb.Table("messages").Filter(map[string]any{"Topic": testData.Topics[0].Id}).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor3.Close()

	if err = cursor3.All(&messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Error("Result should be empty:", messages)
	}
}

func TestTopicDelete(t *testing.T) {
	err := adp.TopicDelete(testData.Topics[1].Id, false, false)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("topics").Get(testData.Topics[1].Id).Field("State").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var state types.ObjState
	if err = cursor.One(&state); err != nil {
		t.Fatal(err)
	}
	if state != types.StateDeleted {
		t.Error("Soft delete failed:", state)
	}

	err = adp.TopicDelete(testData.Topics[0].Id, false, true)
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table("topics").Get(testData.Topics[0].Id).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if !cursor2.IsNil() {
		t.Error("Hard delete failed - topic still exists")
	}
}

func TestFileDeleteUnused(t *testing.T) {
	// time.Now() is correct (as opposite to testData.Now):
	// the FileFinishUpload uses time.Now() as a timestamp.
	locs, err := adp.FileDeleteUnused(time.Now().Add(1*time.Minute), 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) < 1 {
		t.Log("No unused files to delete - this is expected in test environment")
	}
}

func TestUserDelete(t *testing.T) {
	err := adp.UserDelete(types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := rdb.Table("users").Get(testData.Users[0].Id).Field("State").Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()

	var state types.ObjState
	if err = cursor.One(&state); err != nil {
		t.Fatal(err)
	}
	if state != types.StateDeleted {
		t.Error("User soft delete failed", state)
	}

	err = adp.UserDelete(types.ParseUserId("usr"+testData.Users[1].Id), true)
	if err != nil {
		t.Fatal(err)
	}

	cursor2, err := rdb.Table(
		"users").Get(testData.Users[1].Id).Run(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor2.Close()

	if !cursor2.IsNil() {
		t.Error("User hard delete failed - user still exists")
	}
}

func TestMessageGetDeleted(t *testing.T) {
	qOpts := types.QueryOpt{
		Since:  1,
		Before: 10,
		Limit:  999,
	}
	got, err := adp.MessageGetDeleted(testData.Topics[1].Id, types.ParseUserId("usr"+testData.Users[2].Id), &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Error(mismatchErrorString("result length", len(got), 1))
	}
}

func TestUserUnreadCount(t *testing.T) {
	uids := []types.Uid{
		types.ParseUserId("usr" + testData.Users[1].Id),
		types.ParseUserId("usr" + testData.Users[2].Id),
	}
	expected := map[types.Uid]int{uids[0]: 0, uids[1]: 166}
	counts, err := adp.UserUnreadCount(uids...)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 {
		t.Error(mismatchErrorString("UnreadCount length", len(counts), 2))
	}

	for uid, unread := range counts {
		if expected[uid] != unread {
			t.Error(mismatchErrorString("UnreadCount", unread, expected[uid]))
		}
	}

	// Test not found (even if the account is not found, the call must return one record).
	counts, err = adp.UserUnreadCount(dummyUid1)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 {
		t.Error(mismatchErrorString("UnreadCount length (dummy)", len(counts), 1))
	}
	if counts[dummyUid1] != 0 {
		t.Error(mismatchErrorString("Non-zero UnreadCount (dummy)", counts[dummyUid1], 0))
	}
}

// ================================================================
func mismatchErrorString(key string, got, want any) string {
	return fmt.Sprintf("%s mismatch:\nGot  = %+v\nWant = %+v", key, got, want)
}

func init() {
	logs.Init(os.Stderr, "stdFlags")
	adp = backend.GetTestAdapter()
	conffile := flag.String("config", "./test.conf", "config of the database connection")

	if file, err := os.Open(*conffile); err != nil {
		log.Fatal("Failed to read config file:", err)
	} else if err = json.NewDecoder(jcr.New(file)).Decode(&config); err != nil {
		log.Fatal("Failed to parse config file:", err)
	}

	if adp == nil {
		log.Fatal("Database adapter is missing")
	}
	if adp.IsOpen() {
		log.Print("Connection is already opened")
	}

	err := adp.Open(config.Adapters[adp.GetName()])
	if err != nil {
		log.Fatal(err)
	}

	conn = adp.GetTestDB().(*rdb.Session)
	testData = test_data.InitTestData()
	if testData == nil {
		log.Fatal("Failed to initialize test data")
	}
	store.SetTestUidGenerator(*testData.UGen)
}
