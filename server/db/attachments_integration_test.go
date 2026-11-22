package adapter_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	_ "github.com/tinode/chat/server/db/mongodb"
	_ "github.com/tinode/chat/server/db/mysql"
	_ "github.com/tinode/chat/server/db/postgres"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/logs"
	_ "github.com/tinode/chat/server/media/fs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// TestAttachmentLinksIntegration resets the database in the explicitly supplied
// store configuration. Run once per adapter, using its build tag:
// TINODE_ATTACHMENT_TEST_CONFIG=/path/to/isolated-store.json go test -tags postgres ./server/db -run TestAttachmentLinksIntegration -v
// The JSON uses the store_config format (use_adapter, uid_key, adapters).
func TestAttachmentLinksIntegration(t *testing.T) {
	path := os.Getenv("TINODE_ATTACHMENT_TEST_CONFIG")
	if path == "" {
		t.Skip("set TINODE_ATTACHMENT_TEST_CONFIG to an isolated store configuration; its database will be reset")
	}
	conf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	logs.Init(io.Discard, "stdFlags")
	if err := store.Store.InitDb(json.RawMessage(conf), true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Store.Close() })
	// Schema creation may use a bootstrap connection without a default database.
	// Reconnect as the application does before exercising the connection pool.
	if err := store.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Store.Open(1, json.RawMessage(conf)); err != nil {
		t.Fatal(err)
	}
	mediaConf, _ := json.Marshal(map[string]string{"upload_dir": t.TempDir()})
	if err := store.Store.UseMediaHandler("fs", string(mediaConf)); err != nil {
		t.Fatal(err)
	}
	a := store.Store.GetAdapter()
	user := &types.User{ObjHeader: types.ObjHeader{Id: store.Store.GetUidString()}}
	user.InitTimes()
	if err := a.UserCreate(user); err != nil {
		t.Fatal(err)
	}
	topic := &types.Topic{ObjHeader: types.ObjHeader{Id: "grp" + store.Store.GetUidString()}, Owner: user.Id}
	topic.InitTimes()
	topic.TouchedAt = topic.CreatedAt
	if err := a.TopicCreate(topic); err != nil {
		t.Fatal(err)
	}
	seq := 0
	message := func(t *testing.T, urls []string) *types.Message {
		t.Helper()
		seq++
		msg := &types.Message{Topic: topic.Id, SeqId: seq, From: user.Id, Content: "attachment test"}
		if err, _ := store.Messages.Save(msg, urls, false); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	file := func(t *testing.T) *types.FileDef {
		t.Helper()
		fd := &types.FileDef{ObjHeader: types.ObjHeader{Id: store.Store.GetUidString()}, User: user.Id, MimeType: "text/plain", Location: store.Store.GetUidString()}
		fd.InitTimes()
		if err := a.FileStartUpload(fd); err != nil {
			t.Fatal(err)
		}
		return fd
	}
	collect := func(t *testing.T) []string {
		t.Helper()
		locations, err := a.FileDeleteUnused(time.Now().Add(time.Hour), 1000)
		if err != nil {
			t.Fatal(err)
		}
		return locations
	}
	retained := func(t *testing.T, fd *types.FileDef) {
		t.Helper()
		got, err := a.FileGet(fd.Id)
		if err != nil || got == nil {
			t.Fatalf("linked file %s lost: %v", fd.Id, err)
		}
	}
	t.Run("mixed_files_report_all_failures_and_retain_valid_files", func(t *testing.T) {
		msg := message(t, nil)
		first, second := file(t), file(t)
		missing1, missing2 := store.Store.GetUidString(), store.Store.GetUidString()
		failed, err := a.FileLinkAttachments("", types.ZeroUid, msg.Uid(), []string{missing1, first.Id, "malformed", missing2, second.Id, first.Id})
		if want := []string{missing1, "malformed", missing2}; !reflect.DeepEqual(failed, want) || err == nil {
			t.Fatalf("failed=%v err=%v, want %v and error", failed, err, want)
		}
		if !errors.Is(err, types.ErrMalformed) {
			t.Fatalf("missing malformed cause: %v", err)
		}
		// Retrying existing references must not overcount them in document adapters.
		if failed, err := a.FileLinkAttachments("", types.ZeroUid, msg.Uid(), []string{first.Id, second.Id}); err != nil || len(failed) != 0 {
			t.Fatalf("retry failed=%v err=%v", failed, err)
		}
		collect(t)
		retained(t, first)
		retained(t, second)
		if err := a.MessageDeleteList(topic.Id, nil); err != nil {
			t.Fatal(err)
		}
		deleted := collect(t)
		for _, fd := range []*types.FileDef{first, second} {
			if !slices.Contains(deleted, fd.Location) {
				t.Errorf("unlinked file not collected: %s (%v)", fd.Id, deleted)
			}
		}
	})
	t.Run("publish_succeeds_after_missing_attachments", func(t *testing.T) {
		valid := file(t)
		message(t, []string{store.Store.GetUidString(), valid.Id, store.Store.GetUidString()})
		message(t, nil)
		got, err := a.TopicGet(topic.Id)
		if err != nil || got == nil {
			t.Fatalf("topic lookup: %v", err)
		}
		if got.SeqId != seq {
			t.Fatalf("stored sequence=%d, want %d", got.SeqId, seq)
		}
		msgs, err := a.MessageGetAll(topic.Id, types.ZeroUid, nil)
		if err != nil || len(msgs) != 2 {
			t.Fatalf("stored messages=%d err=%v", len(msgs), err)
		}
		collect(t)
		retained(t, valid)
	})
	t.Run("missing_target_does_not_leak_file_reservation", func(t *testing.T) {
		fd := file(t)
		failed, err := a.FileLinkAttachments("", types.ZeroUid, store.Store.GetUid(), []string{fd.Id})
		if err == nil || !reflect.DeepEqual(failed, []string{fd.Id}) {
			t.Fatalf("failed=%v err=%v", failed, err)
		}
		if deleted := collect(t); !slices.Contains(deleted, fd.Location) {
			t.Fatalf("failed link retained upload: %v", deleted)
		}
	})
	t.Run("concurrent_links_keep_reference_counts_balanced", func(t *testing.T) {
		msg, fd := message(t, nil), file(t)
		results := make(chan error, 8)
		for range 8 {
			go func() {
				_, err := a.FileLinkAttachments("", types.ZeroUid, msg.Uid(), []string{fd.Id, fd.Id})
				results <- err
			}()
		}
		for range 8 {
			if err := <-results; err != nil {
				t.Error(err)
			}
		}
		collect(t)
		retained(t, fd)
		if err := a.MessageDeleteList(topic.Id, nil); err != nil {
			t.Fatal(err)
		}
		if deleted := collect(t); !slices.Contains(deleted, fd.Location) {
			t.Fatalf("duplicate references leaked the upload: %v", deleted)
		}
	})
	for _, target := range []string{"topic", "user"} {
		t.Run(target+"_replacement", func(t *testing.T) {
			first := file(t)
			name, uid := topic.Id, types.ZeroUid
			if target == "user" {
				name, uid = "", user.Uid()
			}
			link := func(ids []string) ([]string, error) { return a.FileLinkAttachments(name, uid, types.ZeroUid, ids) }
			if failed, err := link([]string{first.Id}); err != nil {
				t.Fatalf("initial failed=%v err=%v", failed, err)
			}
			missing := store.Store.GetUidString()
			if failed, err := link([]string{missing}); err == nil || !reflect.DeepEqual(failed, []string{missing}) {
				t.Fatalf("failed=%v err=%v", failed, err)
			}
			// Only the first ID is used for topic/user attachments.
			if failed, err := link([]string{first.Id, missing}); err != nil || len(failed) != 0 {
				t.Fatalf("same-avatar failed=%v err=%v", failed, err)
			}
			collect(t)
			retained(t, first)
			second := file(t)
			if failed, err := link([]string{second.Id}); err != nil {
				t.Fatalf("replacement failed=%v err=%v", failed, err)
			}
			deleted := collect(t)
			if !slices.Contains(deleted, first.Location) {
				t.Fatalf("old avatar not released: %v", deleted)
			}
			retained(t, second)
		})
	}
}
