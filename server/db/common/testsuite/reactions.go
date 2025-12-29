package testsuite

import (
	"testing"
	"time"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	types "github.com/tinode/chat/server/store/types"
)

// RunReactionsCRUD runs the shared Reactions CRUD tests used by adapters.
func RunReactionsCRUD(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	// Ensure topic exists (adapter should handle this, but check for sanity)
	if _, err := adp.TopicGet(td.Topics[1].Id); err != nil {
		t.Fatal(err)
	}

	// Use topic[1] which still has messages at this point in the test suite.
	topic := td.Topics[1].Id
	seq := td.Reacts[0].SeqId

	u00 := types.ParseUid(td.Reacts[0].User)
	u10 := types.ParseUid(td.Reacts[1].User)

	// No reactions added yet: should return nil map and no error.
	got, err := adp.ReactionGetAll(topic, u00, false, nil)
	if err == nil || len(got) != 0 {
		t.Error("Expected no reactions for nil opts")
	}

	// Save two identical reactions from two users
	r1 := td.Reacts[0]
	if err := adp.ReactionSave(r1); err != nil {
		t.Fatal(err)
	}

	r2 := td.Reacts[1]
	if err := adp.ReactionSave(r2); err != nil {
		t.Fatal(err)
	}

	opts := &types.QueryOpt{IdRanges: []types.Range{{Low: seq}}}
	reacts, err := adp.ReactionGetAll(topic, u00, false, opts)
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
	reactsChan, err := adp.ReactionGetAll(topic, u00, true, opts)
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
			if len(r.Users) == 1 && r.Users[0] == u00.UserId() {
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
	r1b := td.Reacts[2]
	if err := adp.ReactionSave(r1b); err != nil {
		t.Fatal(err)
	}
	reacts, err = adp.ReactionGetAll(topic, u00, false, opts)
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

	// Delete u0 reaction
	if err := adp.ReactionDelete(topic, seq, u00); err != nil {
		t.Fatal(err)
	}

	reacts, err = adp.ReactionGetAll(topic, u00, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(reacts) != 1 {
		t.Error("Expected reaction to one message only after delete, got", len(reacts))
	}

	// After delete only ❤️ remains
	for _, arr := range reacts {
		if len(arr) != 1 {
			t.Error("Expected only one reaction type after delete, got", len(arr))
		} else if arr[0].Content != "❤️" {
			t.Error("expected only ❤️ reaction")
		}
	}

	// IfModifiedSince filter
	// create old reaction and new reaction on another seq
	seq2 := td.Reacts[3].SeqId
	old := td.Reacts[3]
	newr := td.Reacts[4]
	if err := adp.ReactionSave(old); err != nil {
		t.Fatal(err)
	}
	if err := adp.ReactionSave(newr); err != nil {
		t.Fatal(err)
	}

	ims := time.Now().Add(-30 * time.Minute)
	opts2 := &types.QueryOpt{IdRanges: []types.Range{{Low: seq2}}, IfModifiedSince: &ims}
	reacts, err = adp.ReactionGetAll(topic, u10, false, opts2)
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

	// Check that LIMIT applies to number of messages (seqids), not number of reaction rows
	// Create reactions for two different seqids
	rA1 := td.Reacts[5]
	rA2 := td.Reacts[6]
	rB1 := td.Reacts[7]
	if err := adp.ReactionSave(rA1); err != nil {
		t.Fatal(err)
	}
	if err := adp.ReactionSave(rA2); err != nil {
		t.Fatal(err)
	}
	if err := adp.ReactionSave(rB1); err != nil {
		t.Fatal(err)
	}

	optsLimit := &types.QueryOpt{IdRanges: []types.Range{{Low: td.Reacts[5].SeqId}, {Low: td.Reacts[7].SeqId}}, Limit: 1}
	reactsLimit, err := adp.ReactionGetAll(topic, u00, false, optsLimit)
	if err != nil {
		t.Fatal(err)
	}
	// Expect only one seqid returned (the highest seqid due to ORDER BY seqid DESC in adapter)
	if len(reactsLimit) != 1 {
		t.Fatalf("expected 1 seqid due to limit, got %d", len(reactsLimit))
	}
	for k := range reactsLimit {
		if k != td.Reacts[7].SeqId {
			t.Fatalf("expected seqid %d (highest), got %d", td.Reacts[7].SeqId, k)
		}
	}

	// Invalid QueryOpt (non-nil but no ranges/since/before) => error
	_, err = adp.ReactionGetAll(topic, u00, false, &types.QueryOpt{})
	if err == nil {
		t.Error("expected error for invalid query options")
	}
}
