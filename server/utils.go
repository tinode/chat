// Generic data manipulation utilities.

package main

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

var tagPrefixRegexp = regexp.MustCompile(`^([a-z]\w{0,5}):\S`)

// Convert a list of IDs into ranges
func delrangeDeserialize(in []types.Range) []MsgDelRange {
	if len(in) == 0 {
		return nil
	}

	var out []MsgDelRange
	for _, r := range in {
		out = append(out, MsgDelRange{LowId: r.Low, HiId: r.Hi})
	}

	return out
}

func delrangeSerialize(in []MsgDelRange) []types.Range {
	if len(in) == 0 {
		return nil
	}

	var out []types.Range
	for _, r := range in {
		out = append(out, types.Range{Low: r.LowId, Hi: r.HiId})
	}

	return out
}

// Trim whitespace, remove empty tags and duplicates, ensure proper format of prefixes,
// compare new to old to make sure restricted tags are not removed.
func normalizeTags(dst, src []string) []string {
	if len(src) == 0 {
		return dst
	}

	// Make sure the number of tags does not exceed the maximum.
	// Technically it may result in fewer tags than the maximum due to empty tags and
	// duplicates, but that's user's fault.
	if len(src) > globals.maxTagCount {
		src = src[:globals.maxTagCount]
	}

	// Trim whitespace and force to lowercase.
	for i := 0; i < len(src); i++ {
		src[i] = strings.ToLower(strings.TrimSpace(src[i]))
	}

	// Sort tags
	sort.Strings(src)

	// Remove short tags and de-dupe keeping the order. It may result in fewer tags than could have
	// been if length were enforced later, but that's client's fault.
	var prev string
	for _, curr := range src {
		if len(curr) < minTagLength || curr == prev || isNullValue(curr) {
			continue
		}
		dst = append(dst, curr)
		prev = curr
	}

	return dst
}

// restrictedTagsDelta extracts the lists of added and removed restricted tags:
//   added :=  newTags - (oldTags & newTags) -- present in new but missing in old
//   removed := oldTags - (newTags & oldTags) -- present in old but missing in new
func restrictedTagsDelta(oldTags, newTags []string) (added, removed []string) {
	rold := filterRestrictedTags(oldTags)
	rnew := filterRestrictedTags(newTags)

	if len(rold) == 0 && len(rnew) == 0 {
		return nil, nil
	}
	if len(rold) == 0 {
		return rnew, nil
	}
	if len(rnew) == 0 {
		return nil, rold
	}

	sort.Strings(rold)
	sort.Strings(rnew)

	// Match old tags against the new tags and separate removed tags from added.
	o, n := 0, 0
	lold, lnew := len(rold), len(rnew)
	for o < lold || n < lnew {
		if o == lold || (n < lnew && rold[o] > rnew[n]) {
			// Present in new, missing in old: added
			added = append(added, rnew[n])
			n++

		} else if n == lnew || rold[o] < rnew[n] {
			// Present in old, missing in new: removed
			removed = append(removed, rold[o])
			o++

		} else {
			// present in both
			if o < lold {
				o++
			}
			if n < lnew {
				n++
			}
		}
	}
	return added, removed
}

// restrictedTags checks if two sets of tags contain the same set of restricted tags
func restrictedTags(oldTags, newTags []string) bool {
	rold := filterRestrictedTags(oldTags)
	rnew := filterRestrictedTags(newTags)

	if len(rold) != len(rnew) {
		return false
	}

	sort.Strings(rold)
	sort.Strings(rnew)

	// Match old tags against the new tags.
	for i := 0; i < len(rnew); i++ {
		if rold[i] != rnew[i] {
			return false
		}
	}

	return true
}

// Take a slice of tags, return a slice of restricted tags contained in the input.
func filterRestrictedTags(tags []string) []string {
	var out []string
	if len(globals.restrictedTags) > 0 && len(tags) > 0 {
		for _, s := range tags {
			parts := tagPrefixRegexp.FindStringSubmatch(s)

			if len(parts) < 2 {
				continue
			}

			if globals.restrictedTags[parts[1]] {
				out = append(out, s)
			}
		}
	}
	return out
}

// Takes get.data or get.del parameters, returns database query parameters
func msgOpts2storeOpts(req *MsgBrowseOpts) *types.BrowseOpt {
	var opts *types.BrowseOpt
	if req != nil {
		opts = &types.BrowseOpt{
			Limit:  req.Limit,
			Since:  req.SinceId,
			Before: req.BeforeId,
		}
	}
	return opts
}

// Check if the interface contains a string with a single Unicode Del control character.
func isNullValue(i interface{}) bool {
	const clearValue = "\u2421"
	if str, ok := i.(string); ok {
		return str == clearValue
	}
	return false
}

func decodeAuthError(err error, id string, timestamp time.Time) *ServerComMessage {

	if err == nil {
		return NoErr(id, "", timestamp)
	}

	authErr, ok := err.(auth.AuthErr)
	if !ok {
		return ErrUnknown(id, "", timestamp)
	}

	var errmsg *ServerComMessage

	switch authErr {
	case auth.ErrInternal:
		errmsg = ErrUnknown(id, "", timestamp)
	case auth.ErrMalformed:
		errmsg = ErrMalformed(id, "", timestamp)
	case auth.ErrFailed:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrDuplicate:
		errmsg = ErrDuplicateCredential(id, "", timestamp)
	case auth.ErrUnsupported:
		errmsg = ErrNotImplemented(id, "", timestamp)
	case auth.ErrExpired:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrPolicy:
		errmsg = ErrPolicy(id, "", timestamp)
	default:
		errmsg = ErrUnknown(id, "", timestamp)
	}

	return errmsg
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
