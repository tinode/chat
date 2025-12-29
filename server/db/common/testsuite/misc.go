package testsuite

import (
	"reflect"
	"testing"
	"time"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	"github.com/tinode/chat/server/store/types"
)

func RunUserUpdateTags(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	addTags := td.Tags[0]
	removeTags := td.Tags[1]
	resetTags := td.Tags[2]
	uid := types.ParseUid(td.Users[0].Id)

	got, err := adp.UserUpdateTags(uid, addTags, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alice", "tag1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tags mismatch: got %+v want %+v", got, want)
	}

	got, err = adp.UserUpdateTags(uid, nil, removeTags, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tags mismatch: got %+v want %+v", got, want)
	}

	got, err = adp.UserUpdateTags(uid, nil, nil, resetTags)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"alice", "tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tags mismatch: got %+v want %+v", got, want)
	}

	got, err = adp.UserUpdateTags(uid, addTags, removeTags, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"tag111", "tag333"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tags mismatch: got %+v want %+v", got, want)
	}
}

func RunDeviceGetAll(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	uid0 := types.ParseUid(td.Users[0].Id)
	uid1 := types.ParseUid(td.Users[1].Id)
	uid2 := types.ParseUid(td.Users[2].Id)
	gotDevs, count, err := adp.DeviceGetAll(uid0, uid1, uid2)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count mismatch: got %v want %v", count, 2)
	}
	if !gotDevs[uid1][0].LastSeen.Equal(td.Devs[0].LastSeen) {
		t.Errorf("Device LastSeen mismatch for %v: got %+v want %+v", uid1, gotDevs[uid1][0].LastSeen, td.Devs[0].LastSeen)
	}
	dev0 := gotDevs[uid1][0]
	dev0.LastSeen = td.Devs[0].LastSeen
	if !reflect.DeepEqual(dev0, *td.Devs[0]) {
		t.Errorf("Device mismatch: got %+v want %+v", gotDevs[uid1][0], *td.Devs[0])
	}
	if !gotDevs[uid2][0].LastSeen.Equal(td.Devs[1].LastSeen) {
		t.Errorf("Device LastSeen mismatch for %v: got %+v want %+v", uid2, gotDevs[uid2][0].LastSeen, td.Devs[1].LastSeen)
	}
	dev1 := gotDevs[uid2][0]
	dev1.LastSeen = td.Devs[1].LastSeen
	if !reflect.DeepEqual(dev1, *td.Devs[1]) {
		t.Errorf("Device mismatch: got %+v want %+v", gotDevs[uid2][0], *td.Devs[1])
	}
}

func RunPCacheUpsert(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	if err := adp.PCacheUpsert("test_key", "test_value", false); err != nil {
		t.Fatal(err)
	}
	if err := adp.PCacheUpsert("test_key2", "test_value2", true); err != nil {
		t.Fatal(err)
	}
	if err := adp.PCacheUpsert("test_key2", "new_value", true); err != types.ErrDuplicate {
		t.Error("Expected duplicate error")
	}
}

func RunPCacheGet(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	value, err := adp.PCacheGet("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "test_value" {
		t.Errorf("Cache value mismatch: got %v want %v", value, "test_value")
	}
	// Test not found
	_, err = adp.PCacheGet("nonexistent")
	if err != types.ErrNotFound {
		t.Error("Expected not found error")
	}
}

func RunPCacheDelete(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	if err := adp.PCacheDelete("test_key"); err != nil {
		t.Fatal(err)
	}
	// Verify deleted
	_, err := adp.PCacheGet("test_key")
	if err != types.ErrNotFound {
		t.Error("Key should be deleted")
	}
}

func RunPCacheExpire(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	adp.PCacheUpsert("prefix_key1", "value1", false)
	adp.PCacheUpsert("prefix_key2", "value2", false)
	if err := adp.PCacheExpire("prefix_", time.Now().Add(1*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func RunAuthDelAllRecords(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	delCount, err := adp.AuthDelAllRecords(types.ParseUid(td.Recs[0].UserId))
	if err != nil {
		t.Fatal(err)
	}
	if delCount != 1 {
		t.Errorf("delCount mismatch: got %v want %v", delCount, 1)
	}

	delCount, _ = adp.AuthDelAllRecords(types.ParseUserId("dummyuserid"))
	if delCount != 0 {
		t.Errorf("delCount mismatch: got %v want %v", delCount, 0)
	}
}

func RunUserUnreadCount(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	uids := []types.Uid{
		types.ParseUid(td.Users[1].Id),
		types.ParseUid(td.Users[2].Id),
	}
	expected := map[types.Uid]int{uids[0]: 0, uids[1]: 166}
	counts, err := adp.UserUnreadCount(uids...)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 {
		t.Errorf("UnreadCount length mismatch: got %v want %v", len(counts), 2)
	}
	for uid, unread := range counts {
		if expected[uid] != unread {
			t.Errorf("UnreadCount mismatch for %v: got %v want %v", uid, unread, expected[uid])
		}
	}

	// Test not found
	counts, err = adp.UserUnreadCount(types.Uid(12345))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 {
		t.Errorf("UnreadCount length (dummy) mismatch: got %v want %v", len(counts), 1)
	}
	if counts[types.Uid(12345)] != 0 {
		t.Errorf("Non-zero UnreadCount (dummy): got %v want %v", counts[types.Uid(12345)], 0)
	}
}
