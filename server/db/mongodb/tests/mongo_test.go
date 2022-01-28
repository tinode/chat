// To test another db backend:
// 1) Create GetAdapter function inside your db backend adapter package (like one inside mongodb adapter)
// 2) Uncomment your db backend package ('backend' named package)
// 3) Write own initConnectionToDb and 'db' variable
// 4) Replace mongodb specific db queries inside test to your own queries.
// 5) Run.

package tests

import (
	"bytes"
	"context"
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
	b "go.mongodb.org/mongo-driver/bson"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"

	backend "github.com/tinode/chat/server/db/mongodb"
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
var db *mdb.Database
var ctx context.Context

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
}

// ================== Create tests ================================
func TestUserCreate(t *testing.T) {
	for _, user := range users {
		if err := adp.UserCreate(user); err != nil {
			t.Error(err)
		}
	}
	count, err := db.Collection("users").CountDocuments(ctx, b.M{})
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
		inserted, err := adp.CredUpsert(creds[i])
		if err != nil {
			t.Fatal(err)
		}
		if !inserted {
			t.Error("Should be inserted, but updated")
		}
	}

	// Test duplicate:
	_, err := adp.CredUpsert(creds[1])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}
	_, err = adp.CredUpsert(creds[2])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}

	// Test add new unvalidated credentials
	inserted, err := adp.CredUpsert(creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Error("Should be inserted, but updated")
	}
	inserted, err = adp.CredUpsert(creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Error("Should be updated, but inserted")
	}

	// Just insert other creds (used in other tests)
	for _, cred := range creds[4:] {
		_, err = adp.CredUpsert(cred)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthAddRecord(t *testing.T) {
	for _, rec := range recs {
		err := adp.AuthAddRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, rec.Id,
			rec.AuthLvl, rec.Secret, rec.Expires)
		if err != nil {
			t.Fatal(err)
		}
	}
	//Test duplicate
	err := adp.AuthAddRecord(types.ParseUserId("usr"+users[0].Id), recs[0].Scheme, recs[0].Id,
		recs[0].AuthLvl, recs[0].Secret, recs[0].Expires)
	if err != types.ErrDuplicate {
		t.Fatal("Should be duplicate error but got", err)
	}
}

func TestTopicCreate(t *testing.T) {
	err := adp.TopicCreate(topics[0])
	if err != nil {
		t.Error(err)
	}
	for _, tpc := range topics[3:] {
		err = adp.TopicCreate(tpc)
		if err != nil {
			t.Error(err)
		}
	}
}

func TestTopicCreateP2P(t *testing.T) {
	err := adp.TopicCreateP2P(subs[2], subs[3])
	if err != nil {
		t.Fatal(err)
	}

	oldModeGiven := subs[2].ModeGiven
	subs[2].ModeGiven = 255
	err = adp.TopicCreateP2P(subs[4], subs[2])
	if err != nil {
		t.Fatal(err)
	}
	var got types.Subscription
	err = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": subs[2].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModeGiven == oldModeGiven {
		t.Error("ModeGiven update failed")
	}
}

func TestTopicShare(t *testing.T) {
	if err := adp.TopicShare(subs); err != nil {
		t.Fatal(err)
	}
}

func TestMessageSave(t *testing.T) {
	for _, msg := range msgs {
		err := adp.MessageSave(msg)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileStartUpload(t *testing.T) {
	for _, f := range files {
		err := adp.FileStartUpload(f)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// ================== Read tests ==================================
func TestUserGet(t *testing.T) {
	// Test not found
	got, err := adp.UserGet(types.ParseUserId("dummyuserid"))
	if err == nil && got != nil {
		t.Error("user should be nil.")
	}

	got, err = adp.UserGet(types.ParseUserId("usr" + users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, users[0]) {
		t.Errorf(mismatchErrorString("User", got, users[0]))
	}
}

func TestUserGetAll(t *testing.T) {
	// Test not found
	got, err := adp.UserGetAll(types.ParseUserId("dummyuserid"), types.ParseUserId("otherdummyid"))
	if err == nil && got != nil {
		t.Error("result users should be nil.")
	}

	got, err = adp.UserGetAll(types.ParseUserId("usr"+users[0].Id), types.ParseUserId("usr"+users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatal(mismatchErrorString("resultUsers length", len(got), 2))
	}
	for i, usr := range got {
		if !reflect.DeepEqual(&usr, users[i]) {
			t.Error(mismatchErrorString("User", &usr, users[i]))
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

	got, _ = adp.UserGetByCred(creds[0].Method, creds[0].Value)
	if got != types.ParseUserId("usr"+creds[0].User) {
		t.Error(mismatchErrorString("Uid", got, types.ParseUserId("usr"+creds[0].User)))
	}
}

func TestCredGetActive(t *testing.T) {
	got, err := adp.CredGetActive(types.ParseUserId("usr"+users[2].Id), "tel")
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(got, creds[3]) {
		t.Errorf(mismatchErrorString("Credential", got, creds[3]))
	}

	// Test not found
	_, err = adp.CredGetActive(types.ParseUserId("dummyusrid"), "")
	if err != types.ErrNotFound {
		t.Error("Err should be types.ErrNotFound, but got", err)
	}
}

func TestCredGetAll(t *testing.T) {
	got, err := adp.CredGetAll(types.ParseUserId("usr"+users[2].Id), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf(mismatchErrorString("Credentials length", len(got), 3))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+users[2].Id), "tel", false)
	if len(got) != 2 {
		t.Errorf(mismatchErrorString("Credentials length", len(got), 2))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+users[2].Id), "", true)
	if len(got) != 1 {
		t.Errorf(mismatchErrorString("Credentials length", len(got), 1))
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+users[2].Id), "tel", true)
	if len(got) != 1 {
		t.Errorf(mismatchErrorString("Credentials length", len(got), 1))
	}
}

func TestUserUnreadCount(t *testing.T) {
	count, err := adp.UserUnreadCount(types.ParseUserId("usr" + users[2].Id))
	if err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Error(mismatchErrorString("UnreadCount", count, 100))
	}

	// Test not found
	count, err = adp.UserUnreadCount(types.ParseUserId("dummyuserid"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error(mismatchErrorString("UnreadCount", count, 0))
	}
}

func TestAuthGetUniqueRecord(t *testing.T) {
	uid, authLvl, secret, expires, err := adp.AuthGetUniqueRecord("basic:alice")
	if err != nil {
		t.Fatal(err)
	}
	if uid != types.ParseUserId("usr"+recs[0].UserId) ||
		authLvl != recs[0].AuthLvl ||
		!bytes.Equal(secret, recs[0].Secret) ||
		expires != recs[0].Expires {

		got := fmt.Sprintf("%v %v %v %v", uid, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", recs[0].UserId, recs[0].AuthLvl, recs[0].Secret, recs[0].Expires)
		t.Errorf(mismatchErrorString("Auth record", got, want))
	}

	// Test not found
	uid, _, _, _, err = adp.AuthGetUniqueRecord("qwert:asdfg")
	if err == nil && !uid.IsZero() {
		t.Error("Auth record found but shouldn't. Uid:", uid.String())
	}
}

func TestAuthGetRecord(t *testing.T) {
	recId, authLvl, secret, expires, err := adp.AuthGetRecord(types.ParseUserId("usr"+recs[0].UserId), "basic")
	if err != nil {
		t.Fatal(err)
	}
	if recId != recs[0].Id ||
		authLvl != recs[0].AuthLvl ||
		!bytes.Equal(secret, recs[0].Secret) ||
		expires != recs[0].Expires {

		got := fmt.Sprintf("%v %v %v %v", recId, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", recs[0].Id, recs[0].AuthLvl, recs[0].Secret, recs[0].Expires)
		t.Errorf(mismatchErrorString("Auth record", got, want))
	}

	// Test not found
	recId, _, _, _, err = adp.AuthGetRecord(types.ParseUserId("dummyuserid"), "scheme")
	if err != types.ErrNotFound {
		t.Error("Auth record found but shouldn't. recId:", recId)
	}
}

func TestTopicGet(t *testing.T) {
	got, err := adp.TopicGet(topics[0].Id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, topics[0]) {
		t.Errorf(mismatchErrorString("Topic", got, topics[0]))
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
	gotSubs, err := adp.TopicsForUser(types.ParseUserId("usr"+users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+users[1].Id), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf(mismatchErrorString("Subs length (2)", len(gotSubs), 2))
	}

	qOpts.Topic = ""
	ims := now.Add(15 * time.Minute)
	qOpts.IfModifiedSince = &ims
	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf(mismatchErrorString("Subs length (IMS)", len(gotSubs), 1))
	}

	ims = time.Now().Add(15 * time.Minute)
	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 0 {
		t.Errorf(mismatchErrorString("Subs length (IMS 2)", len(gotSubs), 0))
	}
}

func TestUsersForTopic(t *testing.T) {
	qOpts := types.QueryOpt{
		User:  types.ParseUserId("usr" + users[0].Id),
		Limit: 999,
	}
	gotSubs, err := adp.UsersForTopic("grpgRXf0rU4uR4", false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	gotSubs, err = adp.UsersForTopic("grpgRXf0rU4uR4", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 2))
	}

	gotSubs, err = adp.UsersForTopic("p2p9AVDamaNCRbfKzGSh3mE0w", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 2))
	}
}

func TestOwnTopics(t *testing.T) {
	gotSubs, err := adp.OwnTopics(types.ParseUserId("usr" + users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Fatalf("Got topic length %v instead of %v", len(gotSubs), 1)
	}
	if gotSubs[0] != topics[0].Id {
		t.Errorf("Got topic %v instead of %v", gotSubs[0], topics[0].Id)
	}
}

func TestSubscriptionGet(t *testing.T) {
	got, err := adp.SubscriptionGet(topics[0].Id, types.ParseUserId("usr"+users[0].Id), false)
	if err != nil {
		t.Error(err)
	}
	opts := cmpopts.IgnoreUnexported(types.Subscription{}, types.ObjHeader{})
	if !cmp.Equal(got, subs[0], opts) {
		t.Errorf(mismatchErrorString("Subs", got, subs[0]))
	}
	// Test not found
	got, err = adp.SubscriptionGet("dummytopic", types.ParseUserId("dummyuserid"), false)
	if err != nil {
		t.Error(err)
	}
	if got != nil {
		t.Error("result sub should be nil.")
	}
}

func TestSubsForUser(t *testing.T) {
	gotSubs, err := adp.SubsForUser(types.ParseUserId("usr" + users[0].Id))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 1))
	}

	// Test not found
	gotSubs, err = adp.SubsForUser(types.ParseUserId("usr12345678"))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 0))
	}
}

func TestSubsForTopic(t *testing.T) {
	qOpts := types.QueryOpt{
		User:  types.ParseUserId("usr" + users[0].Id),
		Limit: 999,
	}
	gotSubs, err := adp.SubsForTopic(topics[0].Id, false, &qOpts)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 1))
	}
	// Test not found
	gotSubs, err = adp.SubsForTopic("dummytopicid", false, nil)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Errorf(mismatchErrorString("Subs length", len(gotSubs), 0))
	}
}

func TestFindUsers(t *testing.T) {
	reqTags := [][]string{{"alice", "bob", "carol"}}
	gotSubs, err := adp.FindUsers(types.ParseUserId("usr"+users[2].Id), reqTags, nil)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf(mismatchErrorString("result length", len(gotSubs), 3))
	}
}

func TestFindTopics(t *testing.T) {
	reqTags := [][]string{{"travel", "qwer", "asdf", "zxcv"}}
	gotSubs, err := adp.FindTopics(reqTags, nil)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 3 {
		t.Fatal(mismatchErrorString("result length", len(gotSubs), 3))
	}
}

func TestMessageGetAll(t *testing.T) {
	opts := types.QueryOpt{
		Since:  1,
		Before: 2,
		Limit:  999,
	}
	gotMsgs, err := adp.MessageGetAll(topics[0].Id, types.ParseUserId("usr"+users[0].Id), &opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMsgs) != 1 {
		t.Error(mismatchErrorString("Messages length", len(gotMsgs), 1))
	}
	gotMsgs, _ = adp.MessageGetAll(topics[0].Id, types.ParseUserId("usr"+users[0].Id), nil)
	if len(gotMsgs) != 2 {
		t.Error(mismatchErrorString("Messages length", len(gotMsgs), 2))
	}
	gotMsgs, _ = adp.MessageGetAll(topics[0].Id, types.ZeroUid, nil)
	if len(gotMsgs) != 3 {
		t.Error(mismatchErrorString("Messages length", len(gotMsgs), 3))
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
	update := map[string]interface{}{
		"UserAgent": "Test Agent v0.11",
		"UpdatedAt": now.Add(30 * time.Minute),
	}
	err := adp.UserUpdate(types.ParseUserId("usr"+users[0].Id), update)
	if err != nil {
		t.Fatal(err)
	}

	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[0].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "Test Agent v0.11" {
		t.Errorf(mismatchErrorString("UserAgent", got.UserAgent, "Test Agent v0.11"))
	}
	if got.UpdatedAt == got.CreatedAt {
		t.Error("UpdatedAt field not updated")
	}
}

func TestUserUpdateTags(t *testing.T) {
	addTags := []string{"tag1", "Alice"}
	removeTags := []string{"alice", "tag1", "tag2"}
	resetTags := []string{"Alice", "tag111", "tag333"}
	got, err := adp.UserUpdateTags(types.ParseUserId("usr"+users[0].Id), addTags, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alice", "tag1", "Alice"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(mismatchErrorString("Tags", got, want))

	}
	got, _ = adp.UserUpdateTags(types.ParseUserId("usr"+users[0].Id), nil, removeTags, nil)
	want = []string{"Alice"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(mismatchErrorString("Tags", got, want))

	}
	got, _ = adp.UserUpdateTags(types.ParseUserId("usr"+users[0].Id), nil, nil, resetTags)
	want = []string{"Alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(mismatchErrorString("Tags", got, want))

	}
	got, _ = adp.UserUpdateTags(types.ParseUserId("usr"+users[0].Id), addTags, removeTags, nil)
	want = []string{"Alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(mismatchErrorString("Tags", got, want))

	}
	got, _ = adp.UserUpdateTags(types.ParseUserId("usr"+users[0].Id), addTags, removeTags, nil)
	want = []string{"Alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(mismatchErrorString("Tags", got, want))
	}
}

func TestCredFail(t *testing.T) {
	err := adp.CredFail(types.ParseUserId("usr"+creds[3].User), "tel")
	if err != nil {
		t.Error(err)
	}

	// Check if fields updated
	var got types.Credential
	_ = db.Collection("credentials").FindOne(ctx, b.M{
		"user":   creds[3].User,
		"method": "tel",
		"value":  creds[3].Value}).Decode(&got)
	if got.Retries != 1 {
		t.Errorf(mismatchErrorString("Retries count", got.Retries, 1))
	}
	if got.UpdatedAt == got.CreatedAt {
		t.Error("UpdatedAt field not updated")
	}
}

func TestCredConfirm(t *testing.T) {
	err := adp.CredConfirm(types.ParseUserId("usr"+creds[3].User), "tel")
	if err != nil {
		t.Fatal(err)
	}

	// Test fields are updated
	var got types.Credential
	err = db.Collection("credentials").FindOne(ctx, b.M{
		"user":   creds[3].User,
		"method": "tel",
		"value":  creds[3].Value}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt == got.CreatedAt {
		t.Error("Credential not updated correctly")
	}
	// and uncomfirmed credential deleted
	err = db.Collection("credentials").FindOne(ctx, b.M{"_id": creds[3].User + ":" + got.Method + ":" + got.Value}).Decode(&got)
	if err != mdb.ErrNoDocuments {
		t.Error("Uncomfirmed credential not deleted")
	}
}

func TestAuthUpdRecord(t *testing.T) {
	rec := recs[1]
	newSecret := []byte{'s', 'e', 'c', 'r', 'e', 't'}
	err := adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, rec.Id,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}
	var got authRecord
	err = db.Collection("auth").FindOne(ctx, b.M{"_id": rec.Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Errorf(mismatchErrorString("Secret", got.Secret, rec.Secret))
	}

	// Test with auth ID (unique) change
	newId := "basic:bob12345"
	err = adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, newId,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}
	// Test if old ID deleted
	err = db.Collection("auth").FindOne(ctx, b.M{"_id": rec.Id}).Decode(&got)
	if err == nil || err != mdb.ErrNoDocuments {
		t.Errorf("Unique not changed. Got error: %v; ID: %v", err, got.Id)
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Errorf(mismatchErrorString("Secret", got.Secret, rec.Secret))
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Errorf(mismatchErrorString("Secret", got.Secret, rec.Secret))
	}
}

func TestTopicUpdateOnMessage(t *testing.T) {
	msg := types.Message{
		ObjHeader: types.ObjHeader{
			CreatedAt: now.Add(33 * time.Minute),
		},
		SeqId: 66,
	}
	err := adp.TopicUpdateOnMessage(topics[2].Id, &msg)
	if err != nil {
		t.Fatal(err)
	}
	var got types.Topic
	err = db.Collection("topics").FindOne(ctx, b.M{"_id": topics[2].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.TouchedAt != msg.CreatedAt || got.SeqId != msg.SeqId {
		t.Errorf(mismatchErrorString("TouchedAt", got.TouchedAt, msg.CreatedAt))
		t.Errorf(mismatchErrorString("SeqId", got.SeqId, msg.SeqId))
	}
}

func TestTopicUpdate(t *testing.T) {
	update := map[string]interface{}{
		"UpdatedAt": now.Add(55 * time.Minute),
	}
	err := adp.TopicUpdate(topics[0].Id, update)
	if err != nil {
		t.Fatal(err)
	}
	var got types.Topic
	_ = db.Collection("topics").FindOne(ctx, b.M{"_id": topics[0].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Errorf(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}
}

func TestTopicOwnerChange(t *testing.T) {
	err := adp.TopicOwnerChange(topics[0].Id, types.ParseUserId("usr"+users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	var got types.Topic
	_ = db.Collection("topics").FindOne(ctx, b.M{"_id": topics[0].Id}).Decode(&got)
	if got.Owner != users[1].Id {
		t.Errorf(mismatchErrorString("Owner", got.Owner, users[1].Id))
	}
}

func TestSubsUpdate(t *testing.T) {
	update := map[string]interface{}{
		"UpdatedAt": now.Add(22 * time.Minute),
	}
	err := adp.SubsUpdate(topics[0].Id, types.ParseUserId("usr"+users[0].Id), update)
	if err != nil {
		t.Fatal(err)
	}
	var got types.Subscription
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": topics[0].Id + ":" + users[0].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Errorf(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}

	err = adp.SubsUpdate(topics[1].Id, types.ZeroUid, update)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"topic": topics[1].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Errorf(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}
}

func TestSubsDelete(t *testing.T) {
	err := adp.SubsDelete(topics[1].Id, types.ParseUserId("usr"+users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	var got types.Subscription
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": topics[1].Id + ":" + users[0].Id}).Decode(&got)
	if got.DeletedAt == nil {
		t.Errorf(mismatchErrorString("DeletedAt", got.DeletedAt, nil))
	}
}

func TestDeviceUpsert(t *testing.T) {
	err := adp.DeviceUpsert(types.ParseUserId("usr"+users[0].Id), devs[0])
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[0].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(got.DeviceArray[0], devs[0]) {
		t.Error(mismatchErrorString("Device", got.DeviceArray[0], devs[0]))
	}
	// Test update
	devs[0].Platform = "Web"
	err = adp.DeviceUpsert(types.ParseUserId("usr"+users[0].Id), devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[0].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if got.DeviceArray[0].Platform != "Web" {
		t.Error("Device not updated.", got.DeviceArray[0])
	}
	// Test add same device to another user
	err = adp.DeviceUpsert(types.ParseUserId("usr"+users[1].Id), devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[1].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if got.DeviceArray[0].Platform != "Web" {
		t.Error("Device not updated.", got.DeviceArray[0])
	}

	err = adp.DeviceUpsert(types.ParseUserId("usr"+users[2].Id), devs[1])
	if err != nil {
		t.Error(err)
	}
}

func TestMessageAttachments(t *testing.T) {
	fids := []string{files[0].Id, files[1].Id}
	err := adp.FileLinkAttachments("", types.ZeroUid, types.ParseUid(msgs[1].Id), fids)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]string
	findOpts := mdbopts.FindOne().SetProjection(b.M{"attachments": 1, "_id": 0})
	err = db.Collection("messages").FindOne(ctx, b.M{"_id": msgs[1].Id}, findOpts).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["attachments"], fids) {
		t.Error(mismatchErrorString("Attachments", got["attachments"], fids))
	}
	var got2 map[string]int
	findOpts = mdbopts.FindOne().SetProjection(b.M{"usecount": 1, "_id": 0})
	err = db.Collection("fileuploads").FindOne(ctx, b.M{"_id": files[0].Id}, findOpts).Decode(&got2)
	if err != nil {
		t.Fatal(err)
	}
	if got2["usecount"] != 1 {
		t.Error(mismatchErrorString("UseCount", got2["usecount"], 1))
	}
}

func TestFileFinishUpload(t *testing.T) {
	got, err := adp.FileFinishUpload(files[0], true, 22222)
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
	uid0 := types.ParseUserId("usr" + users[0].Id)
	uid1 := types.ParseUserId("usr" + users[1].Id)
	uid2 := types.ParseUserId("usr" + users[2].Id)
	gotDevs, count, err := adp.DeviceGetAll(uid0, uid1, uid2)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal(mismatchErrorString("count", count, 2))
	}
	if !reflect.DeepEqual(gotDevs[uid1][0], *devs[0]) {
		t.Error(mismatchErrorString("Device", gotDevs[uid1][0], *devs[0]))
	}
	if !reflect.DeepEqual(gotDevs[uid2][0], *devs[1]) {
		t.Error(mismatchErrorString("Device", gotDevs[uid2][0], *devs[1]))
	}
}

func TestDeviceDelete(t *testing.T) {
	err := adp.DeviceDelete(types.ParseUserId("usr"+users[1].Id), devs[0].DeviceId)
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[1].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DeviceArray) != 0 {
		t.Error("Device not deleted:", got.DeviceArray)
	}

	err = adp.DeviceDelete(types.ParseUserId("usr"+users[2].Id), "")
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[2].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DeviceArray) != 0 {
		t.Error("Device not deleted:", got.DeviceArray)
	}
}

// ================== Delete tests ================================
func TestCredDel(t *testing.T) {
	err := adp.CredDel(types.ParseUserId("usr"+users[0].Id), "email", "alice@test.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]interface{}
	cur, err := db.Collection("credentials").Find(ctx, b.M{"method": "email", "value": "alice@test.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("Got result but shouldn't", got)
	}

	err = adp.CredDel(types.ParseUserId("usr"+users[1].Id), "", "")
	if err != nil {
		t.Fatal(err)
	}
	cur, err = db.Collection("credentials").Find(ctx, b.M{"user": users[1].Id})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("Got result but shouldn't", got)
	}
}

func TestAuthDelScheme(t *testing.T) {
	// tested during TestAuthUpdRecord
}

func TestAuthDelAllRecords(t *testing.T) {
	delCount, err := adp.AuthDelAllRecords(types.ParseUserId("usr" + recs[0].UserId))
	if err != nil {
		t.Fatal(err)
	}
	if delCount != 1 {
		t.Errorf(mismatchErrorString("delCount", delCount, 1))
	}

	// With dummy user
	delCount, _ = adp.AuthDelAllRecords(types.ParseUserId("dummyuserid"))
	if delCount != 0 {
		t.Errorf(mismatchErrorString("delCount", delCount, 0))
	}
}

func TestSubsDelForUser(t *testing.T) {
	// Tested during TestUserDelete (both hard and soft deletions)
}

func TestMessageDeleteList(t *testing.T) {
	toDel := types.DelMessage{
		ObjHeader: types.ObjHeader{
			Id:        uGen.GetStr(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		Topic:       topics[1].Id,
		DeletedFor:  users[2].Id,
		DelId:       1,
		SeqIdRanges: []types.Range{{Low: 9}, {Low: 3, Hi: 7}},
	}
	err := adp.MessageDeleteList(toDel.Topic, &toDel)
	if err != nil {
		t.Fatal(err)
	}

	var got []types.Message
	cur, err := db.Collection("messages").Find(ctx, b.M{"topic": toDel.Topic})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	for _, msg := range got {
		if msg.SeqId == 1 && msg.DeletedFor != nil {
			t.Error("Message with SeqID=1 should not be deleted")
		}
		if msg.SeqId == 5 && msg.DeletedFor == nil {
			t.Error("Message with SeqID=5 should be deleted")
		}
		if msg.SeqId == 11 && msg.DeletedFor != nil {
			t.Error("Message with SeqID=11 should not be deleted")
		}
	}
	//
	toDel = types.DelMessage{
		ObjHeader: types.ObjHeader{
			Id:        uGen.GetStr(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		Topic:       topics[0].Id,
		DelId:       3,
		SeqIdRanges: []types.Range{{Low: 1, Hi: 3}},
	}
	err = adp.MessageDeleteList(toDel.Topic, &toDel)
	if err != nil {
		t.Fatal(err)
	}
	cur, err = db.Collection("messages").Find(ctx, b.M{"topic": toDel.Topic})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	for _, msg := range got {
		if msg.Content != nil {
			t.Error("Message not deleted:", msg)
		}
	}

	err = adp.MessageDeleteList(topics[0].Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur, err = db.Collection("messages").Find(ctx, b.M{"topic": topics[0].Id})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("Result should be empty:", got)
	}
}

func TestTopicDelete(t *testing.T) {
	err := adp.TopicDelete(topics[1].Id, false)
	if err != nil {
		t.Fatal()
	}
	var got types.Topic
	cur, err := db.Collection("topics").Find(ctx, b.M{"topic": topics[1].Id})
	if err != nil {
		t.Fatal(err)
	}
	for cur.Next(ctx) {
		if err = cur.Decode(&got); err != nil {
			t.Error(err)
		}
		if got.State != types.StateDeleted {
			t.Error("Soft delete failed:", got)
		}
	}

	err = adp.TopicDelete(topics[0].Id, true)
	if err != nil {
		t.Fatal()
	}

	var got2 []types.Topic
	cur, err = db.Collection("topics").Find(ctx, b.M{"topic": topics[0].Id})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got2); err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Error("Hard delete failed:", got2)
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
	err := adp.UserDelete(types.ParseUserId("usr"+users[0].Id), false)
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[0].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != types.StateDeleted {
		t.Error("User soft delete failed", got)
	}

	err = adp.UserDelete(types.ParseUserId("usr"+users[1].Id), true)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": users[1].Id}).Decode(&got)
	if err != mdb.ErrNoDocuments {
		t.Error("User hard delete failed", err)
	}
}

// ================== Other tests =================================
func TestMessageGetDeleted(t *testing.T) {
	qOpts := types.QueryOpt{
		Since:  1,
		Before: 10,
		Limit:  999,
	}
	got, err := adp.MessageGetDeleted(topics[1].Id, types.ParseUserId("usr"+users[2].Id), &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Error(mismatchErrorString("result length", len(got), 1))
	}
}

// ================================================================
func mismatchErrorString(key string, got, want interface{}) string {
	return fmt.Sprintf("%v mismatch:\nGot  = %v\nWant = %v", key, got, want)
}

func initConnectionToDb() {
	var adpConfig struct {
		Addresses interface{} `json:"addresses,omitempty"`
		Database  string      `json:"database,omitempty"`
	}

	if err := json.Unmarshal(config.Adapters[adp.GetName()], &adpConfig); err != nil {
		log.Fatal("adapter mongodb failed to parse config: " + err.Error())
	}

	var opts mdbopts.ClientOptions

	if adpConfig.Addresses == nil {
		opts.SetHosts([]string{"localhost:27017"})
	} else if host, ok := adpConfig.Addresses.(string); ok {
		opts.SetHosts([]string{host})
	} else if hosts, ok := adpConfig.Addresses.([]string); ok {
		opts.SetHosts(hosts)
	} else {
		log.Fatal("adapter mongodb failed to parse config.Addresses")
	}

	if adpConfig.Database == "" {
		adpConfig.Database = "tinode_test"
	}

	ctx = context.Background()
	conn, err := mdb.Connect(ctx, &opts)
	if err != nil {
		log.Fatal(err)
	}

	db = conn.Database(adpConfig.Database)
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

	if err := uGen.Init(11, []byte("testtesttesttest")); err != nil {
		log.Fatal(err)
	}

	initConnectionToDb()
	initData()
}
