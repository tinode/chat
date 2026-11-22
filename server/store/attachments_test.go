package store

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store/types"
)

type attachmentAdapter struct {
	adapter.Adapter
	failed  []string
	err     error
	seqs    []int
	linked  []string
	saveErr error
	readSeq int
}

func (a *attachmentAdapter) TopicUpdateOnMessage(_ string, msg *types.Message) error {
	a.seqs = append(a.seqs, msg.SeqId)
	return nil
}
func (a *attachmentAdapter) MessageSave(*types.Message) error { return a.saveErr }
func (a *attachmentAdapter) SubsUpdate(_ string, _ types.Uid, update map[string]any) error {
	a.readSeq = update["ReadSeqId"].(int)
	return nil
}
func (a *attachmentAdapter) FileLinkAttachments(_ string, _, _ types.Uid, fids []string) ([]string, error) {
	a.linked = append([]string(nil), fids...)
	return a.failed, a.err
}

type attachmentMedia struct{ media.Handler }

func (attachmentMedia) GetIdFromUrl(url string) types.Uid { return types.ParseUid(url) }

func TestSaveLogsAttachmentFailureAndContinues(t *testing.T) {
	oldAdapter, oldMedia, oldGenerator := adp, mediaHandler, uGen
	oldLogger := logs.Warn
	t.Cleanup(func() { adp, mediaHandler, uGen = oldAdapter, oldMedia, oldGenerator; logs.Warn = oldLogger })
	if err := uGen.Init(1, []byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	ids := []string{types.Uid(1).String(), types.Uid(2).String()}
	a := &attachmentAdapter{failed: ids, err: errors.New("link unavailable")}
	adp, mediaHandler = a, attachmentMedia{}
	var output bytes.Buffer
	logs.Warn = log.New(&output, "", 0)
	for seq := 1; seq <= 2; seq++ {
		err, markedRead := (messagesMapper{}).Save(&types.Message{Topic: "grp-test", SeqId: seq, From: types.Uid(3).String()}, ids, true)
		if err != nil || !markedRead || a.readSeq != seq {
			t.Fatalf("seq %d: err=%v markedRead=%v", seq, err, markedRead)
		}
	}
	for _, text := range append(ids, "grp-test", "seq: 1", "link unavailable") {
		if !strings.Contains(output.String(), text) {
			t.Errorf("log missing %q: %s", text, output.String())
		}
	}
	if len(a.seqs) != 2 || a.seqs[0] != 1 || a.seqs[1] != 2 || len(a.linked) != 2 {
		t.Fatalf("unexpected storage calls: %+v", a)
	}
}

func TestSaveStillReturnsMessageStorageFailure(t *testing.T) {
	oldAdapter, oldGenerator := adp, uGen
	t.Cleanup(func() { adp, uGen = oldAdapter, oldGenerator })
	if err := uGen.Init(1, []byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	want := errors.New("message insert failed")
	a := &attachmentAdapter{saveErr: want}
	adp = a
	if err, markedRead := (messagesMapper{}).Save(&types.Message{Topic: "grp-test", SeqId: 1}, nil, false); !errors.Is(err, want) || markedRead {
		t.Fatalf("err=%v markedRead=%v", err, markedRead)
	}
	if a.linked != nil {
		t.Fatal("must not link files after a failed message insert")
	}
}

func TestFileMapperStillReturnsAttachmentFailure(t *testing.T) {
	oldAdapter, oldMedia := adp, mediaHandler
	t.Cleanup(func() { adp, mediaHandler = oldAdapter, oldMedia })
	want := errors.New("link unavailable")
	adp, mediaHandler = &attachmentAdapter{err: want}, attachmentMedia{}
	if err := (fileMapper{}).LinkAttachments("grp-test", types.ZeroUid, []string{types.Uid(1).String()}); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
