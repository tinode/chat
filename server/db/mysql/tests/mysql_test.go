// To test another db backend:
// 1) Create GetAdapter function inside your db backend adapter package (like one inside mysql adapter)
// 2) Uncomment your db backend package ('backend' named package)
// 3) Write own initConnectionToDb and 'db' variable
// 4) Replace mysql specific db queries inside test to your own queries.
// 5) Run.

package tests

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jmoiron/sqlx"
	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/store"
	jcr "github.com/tinode/jsonco"

	"github.com/tinode/chat/server/db/common/test_data"
	backend "github.com/tinode/chat/server/db/mysql"
	"github.com/tinode/chat/server/logs"
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
var db *sqlx.DB
var testData *test_data.TestData

var dummyUid1 = types.Uid(12345)
var dummyUid2 = types.Uid(54321)

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
	// Saved db is closed, get a fresh one.
	db = adp.GetTestDB().(*sqlx.DB)
}

// ================== Create tests ================================
func TestUserCreate(t *testing.T) {
	for _, user := range testData.Users {
		if err := adp.UserCreate(user); err != nil {
			t.Error(err)
		}
	}
	var count int

	if err := db.Ping(); err != nil {
		logs.Err.Println("Database ping failed:", err)
	}
	err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
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
	for _, tpc := range testData.Topics[3:] {
		err = adp.TopicCreate(tpc)
		if err != nil {
			t.Error(err)
		}
	}
}

func decodeUid(u string) int64 {
	return store.DecodeUid(types.ParseUid(u))
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

	var got types.Subscription
	err = db.QueryRow("SELECT createdat,updatedat,deletedat,userid,topic,delid,recvseqid,readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=? AND userid=?",
		testData.Subs[2].Topic, decodeUid(testData.Subs[2].User)).Scan(&got.CreatedAt,
		&got.UpdatedAt, &got.DeletedAt, &got.User, &got.Topic, &got.DelId, &got.RecvSeqId, &got.ReadSeqId, &got.ModeWant, &got.ModeGiven, &got.Private)
	if err != nil {
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
			"delid":     sub.DelId,
			"recvseqid": sub.RecvSeqId,
			"readseqid": sub.ReadSeqId,
		})
	}

	// Update topic SeqId because it's not saved at creation time but used by the tests.
	for _, tpc := range testData.Topics {
		err := adp.TopicUpdate(tpc.Id, map[string]any{
			"seqid": tpc.SeqId,
			"delid": tpc.DelId,
		})
		if err != nil {
			t.Error(err)
		}
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
	got.CreatedAt = testData.Users[0].CreatedAt
	got.UpdatedAt = testData.Users[0].UpdatedAt

	if !reflect.DeepEqual(got, testData.Users[0]) {
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
		usr.CreatedAt = testData.Users[i].CreatedAt
		usr.UpdatedAt = testData.Users[i].UpdatedAt
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
	if !reflect.DeepEqual(got, testData.Creds[3]) {
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
		Topic: "p2p9AVDamaNCRbfKzGSh3mE0w",
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
	gotSubs, err := adp.UsersForTopic("grpgRXf0rU4uR4", false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	gotSubs, err = adp.UsersForTopic("grpgRXf0rU4uR4", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 2))
	}

	gotSubs, err = adp.UsersForTopic("p2p9AVDamaNCRbfKzGSh3mE0w", false, nil)
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

func TestSubscriptionGet(t *testing.T) {
	got, err := adp.SubscriptionGet(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Error(err)
	}

	if diff := cmp.Diff(got, testData.Subs[0],
		cmpopts.IgnoreUnexported(types.Subscription{}, types.ObjHeader{})); diff != "" {
		t.Error(mismatchErrorString("Subs", diff, ""))
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
		t.Error(mismatchErrorString("Subs length", len(gotSubs), 1))
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
		t.Fatalf("%+v", gotMsgs)
		t.Error(mismatchErrorString("Messages length no opts", len(gotMsgs), 2))
	}
	gotMsgs, _ = adp.MessageGetAll(testData.Topics[0].Id, types.ZeroUid, false, nil)
	if len(gotMsgs) != 3 {
		t.Error(mismatchErrorString("Messages length zero uid", len(gotMsgs), 3))
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

	var got struct {
		UserAgent string
		UpdatedAt time.Time
		CreatedAt time.Time
	}
	err = db.QueryRow("SELECT useragent, updatedat, createdat FROM users WHERE id=?", decodeUid(testData.Users[0].Id)).
		Scan(&got.UserAgent, &got.UpdatedAt, &got.CreatedAt)
	if err != nil {
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
	got, err = adp.UserUpdateTags(uid, nil, removeTags, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = nil
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))

	}
	got, err = adp.UserUpdateTags(uid, nil, nil, resetTags)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))

	}
	got, err = adp.UserUpdateTags(uid, addTags, removeTags, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Error(mismatchErrorString("Tags", got, want))

	}
	got, err = adp.UserUpdateTags(uid, addTags, removeTags, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	var got struct {
		Retries   int
		UpdatedAt time.Time
		CreatedAt time.Time
	}
	err = db.QueryRow("SELECT retries, updatedat, createdat FROM credentials WHERE userid=? AND method=? AND value=?",
		decodeUid(testData.Creds[3].User), "tel", testData.Creds[3].Value).Scan(&got.Retries, &got.UpdatedAt, &got.CreatedAt)
	if err != nil {
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

	// Test fields are updated
	var got struct {
		UpdatedAt time.Time
		CreatedAt time.Time
		Done      bool
	}
	err = db.QueryRow("SELECT updatedat, createdat, done FROM credentials WHERE userid=? AND method=? AND value=?",
		decodeUid(testData.Creds[3].User), "tel", testData.Creds[3].Value).Scan(&got.UpdatedAt, &got.CreatedAt, &got.Done)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Error("Credential not updated correctly")
	}
	if !got.Done {
		t.Error("Credential should be marked as done")
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
	var got []byte
	err = db.QueryRow("SELECT secret FROM auth WHERE uname=?", rec.Unique).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got, rec.Secret) {
		t.Error(mismatchErrorString("Secret", got, rec.Secret))
	}

	// Test with auth ID (unique) change
	newId := "basic:bob12345"
	err = adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, newId,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}
	// Test if old ID deleted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM auth WHERE uname=?", rec.Unique).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Old auth record not deleted")
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
	var got struct {
		TouchedAt time.Time
		SeqId     int
	}
	err = db.QueryRow("SELECT touchedat, seqid FROM topics WHERE name=?", testData.Topics[2].Id).
		Scan(&got.TouchedAt, &got.SeqId)
	if err != nil {
		t.Fatal(err)
	}
	if got.TouchedAt != msg.CreatedAt || got.SeqId != msg.SeqId {
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
	var got time.Time
	err = db.QueryRow("SELECT updatedat FROM topics WHERE name=?", testData.Topics[0].Id).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}
}

func TestTopicOwnerChange(t *testing.T) {
	err := adp.TopicOwnerChange(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	err = db.QueryRow("SELECT owner FROM topics WHERE name=?", testData.Topics[0].Id).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	expectedOwner := decodeUid(testData.Users[1].Id) // Assuming user ID conversion
	if got != expectedOwner {
		t.Error(mismatchErrorString("Owner", got, expectedOwner))
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
	var got time.Time
	err = db.QueryRow("SELECT updatedat FROM subscriptions WHERE topic=? AND userid=?",
		testData.Topics[0].Id, decodeUid(testData.Users[0].Id)).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}

	err = adp.SubsUpdate(testData.Topics[1].Id, types.ZeroUid, update)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT updatedat FROM subscriptions WHERE topic=? LIMIT 1",
		testData.Topics[1].Id).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}
}

func TestSubsDelete(t *testing.T) {
	err := adp.SubsDelete(testData.Topics[1].Id, types.ParseUserId("usr"+testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	var deletedat sql.NullTime
	err = db.QueryRow("SELECT deletedat FROM subscriptions WHERE topic=? AND userid=?",
		testData.Topics[1].Id, decodeUid(testData.Users[0].Id)).Scan(&deletedat)
	if err != nil {
		t.Fatal(err)
	}
	if !deletedat.Valid {
		t.Error("DeletedAt should not be null")
	}
}

func TestDeviceUpsert(t *testing.T) {
	err := adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DeviceId string
		Platform string
	}
	err = db.QueryRow("SELECT deviceid, platform FROM devices WHERE userid=? LIMIT 1",
		decodeUid(testData.Users[0].Id)).Scan(&got.DeviceId, &got.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceId != testData.Devs[0].DeviceId || got.Platform != testData.Devs[0].Platform {
		t.Error(mismatchErrorString("Device", got, testData.Devs[0]))
	}

	// Test update
	testData.Devs[0].Platform = "Web"
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT platform FROM devices WHERE userid=? AND deviceid=?",
		decodeUid(testData.Users[0].Id), testData.Devs[0].DeviceId).Scan(&got.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != "Web" {
		t.Error("Device not updated.", got.Platform)
	}

	// Test add same device to another user
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT platform FROM devices WHERE userid=? AND deviceid=?",
		decodeUid(testData.Users[1].Id), testData.Devs[0].DeviceId).Scan(&got.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != "Web" {
		t.Error("Device not updated.", got.Platform)
	}

	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[2].Id), testData.Devs[1])
	if err != nil {
		t.Error(err)
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

func TestMessageAttachments(t *testing.T) {
	fids := []string{testData.Files[0].Id, testData.Files[1].Id}
	err := adp.FileLinkAttachments("", types.ZeroUid, types.ParseUid(testData.Msgs[1].Id), fids)
	if err != nil {
		t.Fatal(err)
	}
	// Check if attachments were linked (this would require checking filemsglinks table)
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM filemsglinks WHERE msgid=?",
		types.ParseUid(testData.Msgs[1].Id)).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != len(fids) {
		t.Error(mismatchErrorString("Attachments count", count, len(fids)))
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
	if count != 2 {
		t.Fatal(mismatchErrorString("count", count, 2))
	}
	if !reflect.DeepEqual(gotDevs[uid1][0], *testData.Devs[0]) {
		t.Error(mismatchErrorString("Device", gotDevs[uid1][0], *testData.Devs[0]))
	}
	if !reflect.DeepEqual(gotDevs[uid2][0], *testData.Devs[1]) {
		t.Error(mismatchErrorString("Device", gotDevs[uid2][0], *testData.Devs[1]))
	}
}

func TestDeviceDelete(t *testing.T) {
	err := adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0].DeviceId)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM devices WHERE userid=?", testData.Users[1].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Device not deleted:", count)
	}

	err = adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[2].Id), "")
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM devices WHERE userid=?", testData.Users[2].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Device not deleted:", count)
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
	_, err = adp.PCacheGet("nonexistent")
	if err != types.ErrNotFound {
		t.Error("Expected not found error")
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
	// Insert some test keys with prefix
	adp.PCacheUpsert("prefix_key1", "value1", false)
	adp.PCacheUpsert("prefix_key2", "value2", false)

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
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM credentials WHERE method='email' AND value='alice@test.example.com'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Got result but shouldn't", count)
	}

	err = adp.CredDel(types.ParseUserId("usr"+testData.Users[1].Id), "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM credentials WHERE userid=?", testData.Users[1].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Got result but shouldn't", count)
	}
}

func TestAuthDelScheme(t *testing.T) {
	// tested during TestAuthUpdRecord
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

func TestSubsDelForUser(t *testing.T) {
	// Tested during TestUserDelete (both hard and soft deletions)
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

	// Check messages in dellog
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM dellog WHERE topic=? AND deletedfor=?",
		toDel.Topic, decodeUid(toDel.DeletedFor)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("No dellog entries created")
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

	// Check if messages content was cleared
	err = db.QueryRow("SELECT COUNT(*) FROM messages WHERE topic=? AND content IS NOT NULL",
		toDel.Topic).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count > 1 {
		t.Errorf("Messages not properly deleted %d, %s", count, toDel.Topic)
	}

	err = adp.MessageDeleteList(testData.Topics[0].Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM messages WHERE topic=?", testData.Topics[0].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Result should be empty:", count)
	}
}

func TestTopicDelete(t *testing.T) {
	err := adp.TopicDelete(testData.Topics[1].Id, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var state int
	err = db.QueryRow("SELECT state FROM topics WHERE name=?", testData.Topics[1].Id).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	if state != int(types.StateDeleted) {
		t.Error("Soft delete failed:", state)
	}

	err = adp.TopicDelete(testData.Topics[0].Id, false, true)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM topics WHERE name=?", testData.Topics[0].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Hard delete failed:", count)
	}
}

func TestFileDeleteUnused(t *testing.T) {
	locs, err := adp.FileDeleteUnused(time.Now().Add(1*time.Minute), 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Error(mismatchErrorString("Locations length", len(locs), 2))
	}
}

func TestUserDelete(t *testing.T) {
	err := adp.UserDelete(types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Fatal(err)
	}
	var state int
	err = db.QueryRow("SELECT state FROM users WHERE id=?",
		decodeUid(testData.Users[0].Id)).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	if state != int(types.StateDeleted) {
		t.Error("User soft delete failed", state)
	}

	err = adp.UserDelete(types.ParseUserId("usr"+testData.Users[1].Id), true)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", testData.Users[1].Id).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("User hard delete failed")
	}
}

// ================== Other tests =================================

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

func TestReactionsCRUD(t *testing.T) {
	// Use topic[1] which still has messages at this point in the test suite.
	topic := testData.Topics[1].Id
	seq := 1
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

	// Ensure topic exists in DB
	var tcount int
	if err := db.QueryRow("SELECT COUNT(*) FROM topics WHERE name=?", topic).Scan(&tcount); err != nil {
		t.Fatal(err)
	}
	if tcount == 0 {
		t.Fatalf("topic %s not found in DB", topic)
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
	seq2 := 5
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

	db = adp.GetTestDB().(*sqlx.DB)
	testData = test_data.InitTestData()
	if testData == nil {
		log.Fatal("Failed to initialize test data")
	}
	store.SetTestUidGenerator(*testData.UGen)
}
