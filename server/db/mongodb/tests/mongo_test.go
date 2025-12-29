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

	adapter "github.com/tinode/chat/server/db"
	jcr "github.com/tinode/jsonco"
	b "go.mongodb.org/mongo-driver/bson"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"

	"github.com/tinode/chat/server/db/common"
	"github.com/tinode/chat/server/db/common/test_data"
	"github.com/tinode/chat/server/db/common/testsuite"
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
var ctx context.Context
var testData *test_data.TestData
var db *mdb.Database

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
}

// ================== Create tests ================================
func TestUserCreate(t *testing.T) {
	for _, user := range testData.Users {
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
	testsuite.RunCredUpsert(t, adp, testData)
}

func TestAuthAddRecord(t *testing.T) {
	testsuite.RunAuthAddRecord(t, adp, testData)
}

func TestTopicCreate(t *testing.T) {
	testsuite.RunTopicCreate(t, adp, testData)
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
	err = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": testData.Subs[2].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModeGiven == oldModeGiven {
		t.Error("ModeGiven update failed")
	}
}

func TestTopicShare(t *testing.T) {
	testsuite.RunTopicShare(t, adp, testData)
}

func TestMessageSave(t *testing.T) {
	testsuite.RunMessageSave(t, adp, testData)
}

func TestFileStartUpload(t *testing.T) {
	testsuite.RunFileStartUpload(t, adp, testData)
}

// ================== Read tests ==================================
func TestUserGet(t *testing.T) {
	testsuite.RunUserGet(t, adp, testData)
}

func TestUserGetAll(t *testing.T) {
	testsuite.RunUserGetAll(t, adp, testData)
}

func TestUserGetByCred(t *testing.T) {
	testsuite.RunUserGetByCred(t, adp, testData)
}

func TestCredGetActive(t *testing.T) {
	testsuite.RunCredGetActive(t, adp, testData)
}

func TestCredGetAll(t *testing.T) {
	testsuite.RunCredGetAll(t, adp, testData)
}

func TestAuthGetUniqueRecord(t *testing.T) {
	testsuite.RunAuthGetUniqueRecord(t, adp, testData)
}

func TestAuthGetRecord(t *testing.T) {
	testsuite.RunAuthGetRecord(t, adp, testData)
}

func TestTopicGet(t *testing.T) {
	testsuite.RunTopicGet(t, adp, testData)
}

func TestTopicsForUser(t *testing.T) {
	testsuite.RunTopicsForUser(t, adp, testData)
}

func TestUsersForTopic(t *testing.T) {
	testsuite.RunUsersForTopic(t, adp, testData)
}

func TestOwnTopics(t *testing.T) {
	testsuite.RunOwnTopics(t, adp, testData)
}

func TestSubscriptionGet(t *testing.T) {
	testsuite.RunSubscriptionGet(t, adp, testData)
}

func TestSubsForUser(t *testing.T) {
	testsuite.RunSubsForUser(t, adp, testData)
}

func TestSubsForTopic(t *testing.T) {
	testsuite.RunSubsForTopic(t, adp, testData)
}

func TestFind(t *testing.T) {
	testsuite.RunFind(t, adp, testData)
}

func TestMessageGetAll(t *testing.T) {
	testsuite.RunMessageGetAll(t, adp, testData)
}

func TestFileGet(t *testing.T) {
	testsuite.RunFileGet(t, adp)
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

	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[0].Id}).Decode(&got)
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
	testsuite.RunUserUpdateTags(t, adp, testData)
}

func TestCredFail(t *testing.T) {
	err := adp.CredFail(types.ParseUserId("usr"+testData.Creds[3].User), "tel")
	if err != nil {
		t.Error(err)
	}

	// Check if fields updated
	var got types.Credential
	_ = db.Collection("credentials").FindOne(ctx, b.M{
		"user":   testData.Creds[3].User,
		"method": "tel",
		"value":  testData.Creds[3].Value}).Decode(&got)
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
	var got types.Credential
	err = db.Collection("credentials").FindOne(ctx, b.M{
		"user":   testData.Creds[3].User,
		"method": "tel",
		"value":  testData.Creds[3].Value}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Error("Credential not updated correctly")
	}
	// and uncomfirmed credential deleted
	err = db.Collection("credentials").FindOne(ctx, b.M{"_id": testData.Creds[3].User + ":" + got.Method + ":" + got.Value}).Decode(&got)
	if err != mdb.ErrNoDocuments {
		t.Error("Uncomfirmed credential not deleted")
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
	var got common.AuthRecord
	err = db.Collection("auth").FindOne(ctx, b.M{"_id": rec.Unique}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Error(mismatchErrorString("Secret", got.Secret, rec.Secret))
	}

	// Test with auth ID (unique) change
	newId := "basic:bob12345"
	err = adp.AuthUpdRecord(types.ParseUserId("usr"+rec.UserId), rec.Scheme, newId,
		rec.AuthLvl, newSecret, rec.Expires)
	if err != nil {
		t.Fatal(err)
	}
	// Test if old ID deleted
	err = db.Collection("auth").FindOne(ctx, b.M{"_id": rec.Unique}).Decode(&got)
	if err == nil || err != mdb.ErrNoDocuments {
		t.Errorf("Unique not changed. Got error: %v; ID: %v", err, got.Unique)
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Error(mismatchErrorString("Secret", got.Secret, rec.Secret))
	}
	if bytes.Equal(got.Secret, rec.Secret) {
		t.Error(mismatchErrorString("Secret", got.Secret, rec.Secret))
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
	var got types.Topic
	err = db.Collection("topics").FindOne(ctx, b.M{"_id": testData.Topics[2].Id}).Decode(&got)
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
	var got types.Topic
	_ = db.Collection("topics").FindOne(ctx, b.M{"_id": testData.Topics[0].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}
}

func TestTopicOwnerChange(t *testing.T) {
	err := adp.TopicOwnerChange(testData.Topics[0].Id, types.ParseUserId("usr"+testData.Users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	var got types.Topic
	_ = db.Collection("topics").FindOne(ctx, b.M{"_id": testData.Topics[0].Id}).Decode(&got)
	if got.Owner != testData.Users[1].Id {
		t.Error(mismatchErrorString("Owner", got.Owner, testData.Users[1].Id))
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
	var got types.Subscription
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": testData.Topics[0].Id + ":" + testData.Users[0].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}

	err = adp.SubsUpdate(testData.Topics[1].Id, types.ZeroUid, update)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"topic": testData.Topics[1].Id}).Decode(&got)
	if got.UpdatedAt != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got.UpdatedAt, update["UpdatedAt"]))
	}
}

func TestSubsDelete(t *testing.T) {
	err := adp.SubsDelete(testData.Topics[1].Id, types.ParseUserId("usr"+testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	var got types.Subscription
	_ = db.Collection("subscriptions").FindOne(ctx, b.M{"_id": testData.Topics[1].Id + ":" + testData.Users[0].Id}).Decode(&got)
	if got.DeletedAt == nil {
		t.Error(mismatchErrorString("DeletedAt", got.DeletedAt, nil))
	}
}

func TestDeviceUpsert(t *testing.T) {
	err := adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[0].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(got.DeviceArray[0], testData.Devs[0]) {
		t.Error(mismatchErrorString("Device", got.DeviceArray[0], testData.Devs[0]))
	}
	// Test update
	testData.Devs[0].Platform = "Web"
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[0].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[0].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if got.DeviceArray[0].Platform != "Web" {
		t.Error("Device not updated.", got.DeviceArray[0])
	}
	// Test add same device to another user
	err = adp.DeviceUpsert(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0])
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[1].Id}).Decode(&got)
	if err != nil {
		t.Error(err)
	}
	if got.DeviceArray[0].Platform != "Web" {
		t.Error("Device not updated.", got.DeviceArray[0])
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
	var got map[string][]string
	findOpts := mdbopts.FindOne().SetProjection(b.M{"attachments": 1, "_id": 0})
	err = db.Collection("messages").FindOne(ctx, b.M{"_id": testData.Msgs[1].Id}, findOpts).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["attachments"], fids) {
		t.Error(mismatchErrorString("Attachments", got["attachments"], fids))
	}
	var got2 map[string]int
	findOpts = mdbopts.FindOne().SetProjection(b.M{"usecount": 1, "_id": 0})
	err = db.Collection("fileuploads").FindOne(ctx, b.M{"_id": testData.Files[0].Id}, findOpts).Decode(&got2)
	if err != nil {
		t.Fatal(err)
	}
	if got2["usecount"] != 1 {
		t.Error(mismatchErrorString("UseCount", got2["usecount"], 1))
	}
}

func TestFileFinishUpload(t *testing.T) {
	testsuite.RunFileFinishUpload(t, adp, testData)
}

// ================== Other tests =================================
func TestDeviceGetAll(t *testing.T) {
	testsuite.RunDeviceGetAll(t, adp, testData)
}

func TestDeviceDelete(t *testing.T) {
	err := adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[1].Id), testData.Devs[0].DeviceId)
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[1].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DeviceArray) != 0 {
		t.Error("Device not deleted:", got.DeviceArray)
	}

	err = adp.DeviceDelete(types.ParseUserId("usr"+testData.Users[2].Id), "")
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[2].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DeviceArray) != 0 {
		t.Error("Device not deleted:", got.DeviceArray)
	}
}

// ================== Persistent Cache tests ======================
func TestPCacheUpsert(t *testing.T) {
	testsuite.RunPCacheUpsert(t, adp)
}

func TestPCacheGet(t *testing.T) {
	testsuite.RunPCacheGet(t, adp)
}

func TestPCacheDelete(t *testing.T) {
	testsuite.RunPCacheDelete(t, adp)
}

func TestPCacheExpire(t *testing.T) {
	testsuite.RunPCacheExpire(t, adp)
}

// ================== Delete tests ================================
func TestCredDel(t *testing.T) {
	err := adp.CredDel(types.ParseUserId("usr"+testData.Users[0].Id), "email", "alice@test.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
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

	err = adp.CredDel(types.ParseUserId("usr"+testData.Users[1].Id), "", "")
	if err != nil {
		t.Fatal(err)
	}
	cur, err = db.Collection("credentials").Find(ctx, b.M{"user": testData.Users[1].Id})
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
	testsuite.RunAuthDelAllRecords(t, adp, testData)
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
	cur, err = db.Collection("messages").Find(ctx, b.M{"topic": toDel.Topic})
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.All(ctx, &got); err != nil {
		t.Fatal(err)
	}
	for _, msg := range got {
		if msg.Content != nil && msg.SeqId != 3 {
			t.Error("Message not deleted:", msg.SeqId)
		}
	}

	err = adp.MessageDeleteList(testData.Topics[0].Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur, err = db.Collection("messages").Find(ctx, b.M{"topic": testData.Topics[0].Id})
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
	err := adp.TopicDelete(testData.Topics[1].Id, false, false)
	if err != nil {
		t.Fatal()
	}
	var got types.Topic
	cur, err := db.Collection("topics").Find(ctx, b.M{"topic": testData.Topics[1].Id})
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

	err = adp.TopicDelete(testData.Topics[0].Id, true, true)
	if err != nil {
		t.Fatal()
	}

	var got2 []types.Topic
	cur, err = db.Collection("topics").Find(ctx, b.M{"topic": testData.Topics[0].Id})
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
	testsuite.RunFileDeleteUnused(t, adp)
}

func TestUserDelete(t *testing.T) {
	err := adp.UserDelete(types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Fatal(err)
	}
	var got types.User
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[0].Id}).Decode(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != types.StateDeleted {
		t.Error("User soft delete failed", got)
	}

	err = adp.UserDelete(types.ParseUserId("usr"+testData.Users[1].Id), true)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Collection("users").FindOne(ctx, b.M{"_id": testData.Users[1].Id}).Decode(&got)
	if err != mdb.ErrNoDocuments {
		t.Error("User hard delete failed", err)
	}
}

func TestUserUnreadCount(t *testing.T) {
	testsuite.RunUserUnreadCount(t, adp, testData)
}

// ================== Other tests =================================

func TestMessageGetDeleted(t *testing.T) {
	testsuite.RunMessageGetDeleted(t, adp, testData)
}

// ================================================================
func mismatchErrorString(key string, got, want any) string {
	return fmt.Sprintf("%s mismatch:\nGot  = %+v\nWant = %+v", key, got, want)
}

func TestReactionsCRUD(t *testing.T) {
	testsuite.RunReactionsCRUD(t, adp, testData)
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

	db = adp.GetTestDB().(*mdb.Database)
	testData = test_data.InitTestData()
	if testData == nil {
		log.Fatal("Failed to initialize test data")
	}
}
