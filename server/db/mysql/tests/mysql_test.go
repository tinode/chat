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
	"github.com/jmoiron/sqlx"
	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/store"
	jcr "github.com/tinode/jsonco"

	"github.com/tinode/chat/server/db/common/test_data"
	"github.com/tinode/chat/server/db/common/testsuite"
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
	testsuite.RunCredUpsert(t, adp, testData)
}

func TestAuthAddRecord(t *testing.T) {
	testsuite.RunAuthAddRecord(t, adp, testData)
}

func TestTopicCreate(t *testing.T) {
	testsuite.RunTopicCreate(t, adp, testData)
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
	err := adp.UserUpdate(types.ParseUid(testData.Users[0].Id), update)
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
	uid := types.ParseUid(testData.Users[0].Id)

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
	err := adp.CredFail(types.ParseUid(testData.Creds[3].User), "tel")
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
	err := adp.CredConfirm(types.ParseUid(testData.Creds[3].User), "tel")
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
	err := adp.AuthUpdRecord(types.ParseUid(rec.UserId), rec.Scheme, rec.Unique,
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
	err = adp.AuthUpdRecord(types.ParseUid(rec.UserId), rec.Scheme, newId,
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
	err := adp.TopicOwnerChange(testData.Topics[0].Id, types.ParseUid(testData.Users[1].Id))
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
	err := adp.SubsUpdate(testData.Topics[0].Id, types.ParseUid(testData.Users[0].Id), update)
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
	err := adp.SubsDelete(testData.Topics[1].Id, types.ParseUid(testData.Users[0].Id))
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
	err := adp.DeviceUpsert(types.ParseUid(testData.Users[0].Id), testData.Devs[0])
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
	err = adp.DeviceUpsert(types.ParseUid(testData.Users[0].Id), testData.Devs[0])
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
	err = adp.DeviceUpsert(types.ParseUid(testData.Users[1].Id), testData.Devs[0])
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

	err = adp.DeviceUpsert(types.ParseUid(testData.Users[2].Id), testData.Devs[1])
	if err != nil {
		t.Error(err)
	}
}

func TestFileFinishUpload(t *testing.T) {
	testsuite.RunFileFinishUpload(t, adp, testData)
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
	testsuite.RunDeviceGetAll(t, adp, testData)
}

func TestDeviceDelete(t *testing.T) {
	err := adp.DeviceDelete(types.ParseUid(testData.Users[1].Id), testData.Devs[0].DeviceId)
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

	err = adp.DeviceDelete(types.ParseUid(testData.Users[2].Id), "")
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
	err := adp.CredDel(types.ParseUid(testData.Users[0].Id), "email", "alice@test.example.com")
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

	err = adp.CredDel(types.ParseUid(testData.Users[1].Id), "", "")
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
	testsuite.RunFileDeleteUnused(t, adp)
}

func TestUserDelete(t *testing.T) {
	err := adp.UserDelete(types.ParseUid(testData.Users[0].Id), false)
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

	err = adp.UserDelete(types.ParseUid(testData.Users[1].Id), true)
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
	testsuite.RunUserUnreadCount(t, adp, testData)
}

func TestMessageGetDeleted(t *testing.T) {
	testsuite.RunMessageGetDeleted(t, adp, testData)
}

func TestReactionsCRUD(t *testing.T) {
	testsuite.RunReactionsCRUD(t, adp, testData)
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
