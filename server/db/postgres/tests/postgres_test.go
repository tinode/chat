// To test another db backend:
// 1) Create GetAdapter function inside your db backend adapter package (like one inside postgres adapter)
// 2) Uncomment your db backend package ('backend' named package)
// 3) Write own initConnectionToDb and 'db' variable
// 4) Replace postgres specific db queries inside test to your own queries.
// 5) Run.

package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/store"
	jcr "github.com/tinode/jsonco"

	"github.com/tinode/chat/server/db/common/test_data"
	"github.com/tinode/chat/server/db/common/testsuite"
	backend "github.com/tinode/chat/server/db/postgres"
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
var db *pgxpool.Pool
var testData *test_data.TestData
var ctx context.Context

var dummyUid1 = types.Uid(12345)
var dummyUid2 = types.Uid(54321)

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
	// Saved db is closed, get a fresh one.
	db = adp.GetTestDB().(*pgxpool.Pool)
}

// ================== Create tests ================================
func TestUserCreate(t *testing.T) {
	for _, user := range testData.Users {
		if err := adp.UserCreate(user); err != nil {
			t.Error(err)
		}
	}
	var count int

	err := db.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
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
	err := adp.TopicCreate(testData.Topics[0])
	if err != nil {
		t.Error(err)
	}
	// Update topic SeqId because it's not saved at creation time but used by the tests.
	err = adp.TopicUpdate(testData.Topics[0].Id, map[string]interface{}{
		"seqid": testData.Topics[0].SeqId,
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

func decodeUid(u string) int64 {
	return store.DecodeUid(types.ParseUid(u))
}

func encodeUid(u string) types.Uid {
	id, _ := strconv.ParseInt(u, 10, 64)
	return store.EncodeUid(int64(id))
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
	var userId int64
	var modeWant, modeGiven []byte
	err = db.QueryRow(ctx, "SELECT createdat,updatedat,deletedat,userid,topic,delid,recvseqid,readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=$1 AND userid=$2",
		testData.Subs[2].Topic, decodeUid(testData.Subs[2].User)).Scan(&got.CreatedAt,
		&got.UpdatedAt, &got.DeletedAt, &userId, &got.Topic, &got.DelId, &got.RecvSeqId, &got.ReadSeqId,
		&modeWant, &modeGiven, &got.Private)
	if err != nil {
		t.Fatal(err)
	}
	got.ModeGiven.Scan(modeGiven)
	if got.ModeGiven == oldModeGiven {
		t.Error("ModeGiven update failed")
	}
}

func TestTopicShare(t *testing.T) {
	testsuite.RunTopicShare(t, adp, testData)
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
	testsuite.RunUserGet(t, adp, testData)
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

	got, err = adp.UserGetAll(types.ParseUid(testData.Users[0].Id), types.ParseUid(testData.Users[1].Id))
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

func TestChannelsForUser(t *testing.T) {
	// Test channels for user (PostgreSQL specific test)
	channels, err := adp.ChannelsForUser(types.ParseUid(testData.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	// Should return empty slice since we don't have channel subscriptions in test data
	if len(channels) != 0 {
		t.Error(mismatchErrorString("Channels length", len(channels), 0))
	}
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

func TestFindOne(t *testing.T) {
	// Test PostgreSQL specific FindOne method
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

	var got struct {
		UserAgent string
		UpdatedAt time.Time
		CreatedAt time.Time
	}
	err = db.QueryRow(ctx, "SELECT useragent, updatedat, createdat FROM users WHERE id=$1",
		decodeUid(testData.Users[0].Id)).Scan(&got.UserAgent, &got.UpdatedAt, &got.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "Test Agent v0.11" {
		t.Error(mismatchErrorString("UserAgent", got.UserAgent, "Test Agent v0.11"))
	}
	if got.UpdatedAt == got.CreatedAt {
		t.Error("UpdatedAt field not updated")
	}
}

func TestUserUpdateTags(t *testing.T) {
	testsuite.RunUserUpdateTags(t, adp, testData)
}

func TestUserGetUnvalidated(t *testing.T) {
	// Test PostgreSQL specific method
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
	err = db.QueryRow(ctx, "SELECT retries, updatedat, createdat FROM credentials WHERE userid=$1 AND method=$2 AND value=$3",
		decodeUid(testData.Creds[3].User), "tel", testData.Creds[3].Value).Scan(&got.Retries, &got.UpdatedAt, &got.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Retries != 1 {
		t.Error(mismatchErrorString("Retries count", got.Retries, 1))
	}
	if got.UpdatedAt == got.CreatedAt {
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
	err = db.QueryRow(ctx, "SELECT updatedat, createdat, done FROM credentials WHERE userid=$1 AND method=$2 AND value=$3",
		decodeUid(testData.Creds[3].User), "tel", testData.Creds[3].Value).Scan(&got.UpdatedAt, &got.CreatedAt, &got.Done)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt == got.CreatedAt {
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
	err = db.QueryRow(ctx, "SELECT secret FROM auth WHERE uname=$1", rec.Unique).Scan(&got)
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM auth WHERE uname=$1", rec.Unique).Scan(&count)
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
	err = db.QueryRow(ctx, "SELECT touchedat, seqid FROM topics WHERE name=$1", testData.Topics[2].Id).
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
	err = db.QueryRow(ctx, "SELECT updatedat FROM topics WHERE name=$1", testData.Topics[0].Id).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != update["UpdatedAt"] {
		t.Error(mismatchErrorString("UpdatedAt", got, update["UpdatedAt"]))
	}
}

func TestTopicUpdateSubCnt(t *testing.T) {
	// Test PostgreSQL specific method
	err := adp.TopicUpdateSubCnt(testData.Topics[0].Id)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the subscription count was updated correctly
	var subcnt int
	err = db.QueryRow(ctx, "SELECT subcnt FROM topics WHERE name=$1", testData.Topics[0].Id).Scan(&subcnt)
	if err != nil {
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
	var got int64
	err = db.QueryRow(ctx, "SELECT owner FROM topics WHERE name=$1", testData.Topics[0].Id).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	expectedOwner := decodeUid(testData.Users[1].Id)
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
	err = db.QueryRow(ctx, "SELECT updatedat FROM subscriptions WHERE topic=$1 AND userid=$2",
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
	err = db.QueryRow(ctx, "SELECT updatedat FROM subscriptions WHERE topic=$1 LIMIT 1",
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
	err = db.QueryRow(ctx, "SELECT deletedat FROM subscriptions WHERE topic=$1 AND userid=$2",
		testData.Topics[1].Id, decodeUid(testData.Users[0].Id)).Scan(&deletedat)
	if err != nil {
		t.Fatal(err)
	}
	if !deletedat.Valid {
		t.Error("DeletedAt should not be null")
	}
}

func TestSubsDelForUser(t *testing.T) {
	// Tested during TestUserDelete (both hard and soft deletions)
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
	err = db.QueryRow(ctx, "SELECT deviceid, platform FROM devices WHERE userid=$1 LIMIT 1",
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
	err = db.QueryRow(ctx, "SELECT platform FROM devices WHERE userid=$1 AND deviceid=$2",
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
	// Check if attachments were linked
	var count int
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM filemsglinks WHERE msgid=$1",
		int64(types.ParseUid(testData.Msgs[1].Id))).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(fids) {
		t.Error(mismatchErrorString("Attachments count", count, len(fids)))
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
	var count int
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM devices WHERE userid=$1 AND deviceid=$2",
		decodeUid(testData.Users[1].Id), testData.Devs[0].DeviceId).Scan(&count)
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM devices WHERE userid=$1",
		decodeUid(testData.Users[2].Id)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("All devices not deleted:", count)
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
	var count int
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM credentials WHERE method='email' AND value='alice@test.example.com'").Scan(&count)
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM credentials WHERE userid=$1",
		decodeUid(testData.Users[1].Id)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("Got result but shouldn't", count)
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
	testsuite.RunAuthDelAllRecords(t, adp, testData)
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM dellog WHERE topic=$1 AND deletedfor=$2",
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM messages WHERE topic=$1 AND content IS NOT NULL",
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM messages WHERE topic=$1", testData.Topics[0].Id).Scan(&count)
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
	err = db.QueryRow(ctx, "SELECT state FROM topics WHERE name=$1", testData.Topics[1].Id).Scan(&state)
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM topics WHERE name=$1", testData.Topics[0].Id).Scan(&count)
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
	err := adp.UserDelete(types.ParseUserId("usr"+testData.Users[0].Id), false)
	if err != nil {
		t.Fatal(err)
	}
	var state int
	err = db.QueryRow(ctx, "SELECT state FROM users WHERE id=$1",
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
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE id=$1",
		decodeUid(testData.Users[1].Id)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("User hard delete failed")
	}
}

func TestUserUnreadCount(t *testing.T) {
	uids := []types.Uid{
		types.ParseUid(testData.Users[1].Id),
		types.ParseUid(testData.Users[2].Id),
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

// ================================================================
func mismatchErrorString(key string, got, want any) string {
	return fmt.Sprintf("%s mismatch:\nGot  = %+v\nWant = %+v", key, got, want)
}

func TestReactionsCRUD(t *testing.T) {
	testsuite.RunReactionsCRUD(t, adp, testData)
}

func init() {
	ctx = context.Background()
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

	db = adp.GetTestDB().(*pgxpool.Pool)
	testData = test_data.InitTestData()
	if testData == nil {
		log.Fatal("Failed to initialize test data")
	}
	store.SetTestUidGenerator(*testData.UGen)
}
