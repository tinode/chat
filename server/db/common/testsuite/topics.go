package testsuite

import (
	"reflect"
	"testing"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	types "github.com/tinode/chat/server/store/types"
)

func RunTopicGet(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	got, err := adp.TopicGet(td.Topics[0].Id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, td.Topics[0]) {
		t.Errorf("Topic mismatch: got %+v want %+v", got, td.Topics[0])
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

func RunTopicCreate(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	err := adp.TopicCreate(td.Topics[0])
	if err != nil {
		t.Error(err)
	}
	for _, tpc := range td.Topics[3:] {
		err = adp.TopicCreate(tpc)
		if err != nil {
			t.Error(err)
		}
	}
}

func RunTopicShare(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	if err := adp.TopicShare(td.Subs[0].Topic, td.Subs); err != nil {
		t.Fatal(err)
	}

	// Must save recvseqid and readseqid separately because TopicShare
	// ignores them.
	for _, sub := range td.Subs {
		adp.SubsUpdate(sub.Topic, types.ParseUid(sub.User), map[string]any{
			"delid":     sub.DelId,
			"recvseqid": sub.RecvSeqId,
			"readseqid": sub.ReadSeqId,
		})
	}

	// Update topic SeqId because it's not saved at creation time but used by the tests.
	for _, tpc := range td.Topics {
		err := adp.TopicUpdate(tpc.Id, map[string]any{
			"seqid": tpc.SeqId,
			"delid": tpc.DelId,
		})
		if err != nil {
			t.Error(err)
		}
	}
}

func RunTopicsForUser(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	qOpts := types.QueryOpt{Topic: td.Topics[1].Id, Limit: 999}
	gotSubs, err := adp.TopicsForUser(types.ParseUserId("usr"+td.Users[0].Id), false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 1)
	}

	gotSubs, err = adp.TopicsForUser(types.ParseUserId("usr"+td.Users[1].Id), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf("Subs length mismatch (2): got %v want %v", len(gotSubs), 2)
	}
}

func RunUsersForTopic(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	qOpts := types.QueryOpt{User: types.ParseUid(td.Users[0].Id), Limit: 999}
	gotSubs, err := adp.UsersForTopic(td.Topics[0].Id, false, &qOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 1)
	}

	gotSubs, err = adp.UsersForTopic(td.Topics[0].Id, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 2)
	}
}

func RunOwnTopics(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	gotSubs, err := adp.OwnTopics(types.ParseUserId("usr" + td.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 {
		t.Fatalf("Got topic length %v instead of %v", len(gotSubs), 1)
	}
	if gotSubs[0] != td.Topics[0].Id {
		t.Errorf("Got topic %v instead of %v", gotSubs[0], td.Topics[0].Id)
	}
}

func RunSubscriptionGet(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	got, err := adp.SubscriptionGet(td.Topics[0].Id, types.ParseUserId("usr"+td.Users[0].Id), false)
	if err != nil {
		t.Error(err)
	}
	if got == nil {
		t.Error("result sub should not be nil")
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

func RunSubsForUser(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	gotSubs, err := adp.SubsForUser(types.ParseUid(td.Users[0].Id))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 2 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 2)
	}

	// Test not found
	gotSubs, err = adp.SubsForUser(types.ParseUserId("usr12345678"))
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 0)
	}
}

func RunSubsForTopic(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	qOpts := types.QueryOpt{User: types.ParseUid(td.Users[0].Id), Limit: 999}
	gotSubs, err := adp.SubsForTopic(td.Topics[0].Id, false, &qOpts)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 1 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 1)
	}
	// Test not found
	gotSubs, err = adp.SubsForTopic("dummytopicid", false, nil)
	if err != nil {
		t.Error(err)
	}
	if len(gotSubs) != 0 {
		t.Errorf("Subs length mismatch: got %v want %v", len(gotSubs), 0)
	}
}
