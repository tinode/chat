package common

import (
	"errors"
	"reflect"
	"testing"

	t "github.com/tinode/chat/server/store/types"
)

func TestLinkAttachments(tst *testing.T) {
	ids := []string{t.Uid(1).String(), t.Uid(2).String(), t.Uid(3).String(), t.Uid(4).String()}
	broken := errors.New("link failed")
	var called []string
	failed, err := LinkAttachments("", t.ZeroUid, t.Uid(1),
		[]string{ids[0], ids[1], "malformed", ids[2], ids[3], ids[0]}, func(fid string) error {
			called = append(called, fid)
			if fid == ids[0] || fid == ids[2] {
				return broken
			}
			return nil
		})
	if !reflect.DeepEqual(called, ids) {
		tst.Fatalf("attempted %v, want %v", called, ids)
	}
	if want := []string{ids[0], "malformed", ids[2]}; !reflect.DeepEqual(failed, want) {
		tst.Fatalf("failed %v, want %v", failed, want)
	}
	if !errors.Is(err, broken) || !errors.Is(err, t.ErrMalformed) {
		tst.Fatalf("missing failure causes: %v", err)
	}
	for _, target := range []struct {
		topic     string
		user, msg t.Uid
	}{
		{topic: "grp-test"}, {user: t.Uid(1)}, {msg: t.Uid(1)},
	} {
		called = nil
		failed, err = LinkAttachments(target.topic, target.user, target.msg, ids, func(fid string) error {
			called = append(called, fid)
			return nil
		})
		want := ids
		if target.msg.IsZero() {
			want = ids[:1]
		}
		if failed != nil || err != nil || !reflect.DeepEqual(called, want) {
			tst.Fatalf("target %+v: called=%v failed=%v err=%v", target, called, failed, err)
		}
	}
}

func TestLinkAttachmentsInvalidTarget(tst *testing.T) {
	ids := []string{t.Uid(1).String(), t.Uid(2).String()}
	failed, err := LinkAttachments("", t.ZeroUid, t.ZeroUid, ids, func(string) error {
		tst.Fatal("must not attempt links without a target")
		return nil
	})
	if !reflect.DeepEqual(failed, ids) || !errors.Is(err, t.ErrMalformed) {
		tst.Fatalf("failed=%v err=%v", failed, err)
	}
}
