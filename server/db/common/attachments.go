package common

import (
	"errors"
	"fmt"

	t "github.com/tinode/chat/server/store/types"
)

// LinkAttachments attempts each distinct file independently, including files after
// a failed link. The callback must not undo earlier successful links on failure.
func LinkAttachments(topic string, userId, msgId t.Uid, fids []string, link func(string) error) ([]string, error) {
	if len(fids) == 0 || (topic == "" && userId.IsZero() && msgId.IsZero()) {
		return fids, t.ErrMalformed
	}
	if msgId.IsZero() {
		// Topics and users support one attachment.
		fids = fids[:1]
	}
	var failed []string
	var errs []error
	seen := make(map[string]bool, len(fids))
	for _, fid := range fids {
		if seen[fid] {
			continue
		}
		seen[fid] = true
		var err error = t.ErrMalformed
		if !t.ParseUid(fid).IsZero() {
			err = link(fid)
		}
		if err != nil {
			failed = append(failed, fid)
			errs = append(errs, fmt.Errorf("file %q: %w", fid, err))
		}
	}
	return failed, errors.Join(errs...)
}
