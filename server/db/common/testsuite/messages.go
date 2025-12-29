package testsuite

import (
	"testing"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	types "github.com/tinode/chat/server/store/types"
)

// RunFind runs the shared Find tests used by adapters.
func RunFind(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	reqTags := [][]string{{"alice", "bob", "carol", "travel", "qwer", "asdf", "zxcv"}}
	got, err := adp.Find("usr"+td.Users[2].Id, "", reqTags, nil, true)
	if err != nil {
		t.Error(err)
	}
	if len(got) != 3 {
		t.Errorf("result length mismatch: got %v want %v", len(got), 3)
	}
}

// RunMessageGetAll runs the shared MessageGetAll tests used by adapters.
func RunMessageGetAll(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	opts := types.QueryOpt{
		Since:  1,
		Before: 2,
		Limit:  999,
	}

	gotMsgs, err := adp.MessageGetAll(td.Topics[0].Id, types.ParseUid(td.Users[0].Id), false, &opts)
	if err != nil {
		t.Fatalf("Error getting messages with opts: %v", err)
	}
	if len(gotMsgs) != 1 {
		t.Errorf("Messages length opts mismatch: got %v want %v", len(gotMsgs), 1)
	}

	gotMsgs, err = adp.MessageGetAll(td.Topics[0].Id, types.ParseUid(td.Users[0].Id), false, nil)
	if err != nil {
		t.Fatalf("Error getting messages without opts: %v", err)
	}

	if len(gotMsgs) != 2 {
		t.Errorf("Messages length no opts mismatch: got %v want %v", len(gotMsgs), 2)
		t.Fatalf("Got messages %+v", gotMsgs)
	}

	gotMsgs, _ = adp.MessageGetAll(td.Topics[0].Id, types.ZeroUid, false, nil)
	if len(gotMsgs) != 3 {
		t.Errorf("Messages length zero uid mismatch: got %v want %v", len(gotMsgs), 3)
	}
}

// RunMessageGetDeleted runs the shared MessageGetDeleted tests used by adapters.
func RunMessageGetDeleted(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	qOpts := types.QueryOpt{
		Since:  1,
		Before: 10,
		Limit:  999,
	}
	got, err := adp.MessageGetDeleted(td.Topics[1].Id, types.ParseUid(td.Users[2].Id), &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("result length mismatch: got %v want %v", len(got), 1)
	}
}

// RunMessageSave runs the shared MessageSave tests used by adapters.
func RunMessageSave(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	for _, msg := range td.Msgs {
		err := adp.MessageSave(msg)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Some messages are soft deleted, but it's ignored by adp.MessageSave
	for _, msg := range td.Msgs {
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
