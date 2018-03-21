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

// Trim whitespace, remove short/empty tags and duplicates, convert to lowercase, ensure
// the number of tags does not exceed the maximum.
func normalizeTags(src []string) []string {
	if len(src) == 0 {
		return nil
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
	var dst []string
	for _, curr := range src {
		if isNullValue(curr) {
			// Return non-nil empty array
			return make([]string, 0, 1)
		}

		if len(curr) < minTagLength || curr == prev {
			continue
		}

		dst = append(dst, curr)
		prev = curr
	}

	return dst
}

// stringDelta extracts the slices of added and removed strings from two slices:
//   added :=  newSlice - (oldSlice & newSlice) -- present in new but missing in old
//   removed := oldSlice - (oldSlice & newSlice) -- present in old but missing in new
func stringSliceDelta(rold, rnew []string) (added, removed []string) {

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

	// Match old slice against the new slice and separate removed strings from added.
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

// restrictedTags checks if two sets of tags contain the same set of restricted tags:
// true - same, false - different.
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

// Process credentials for correctness: remove duplicates and unknown methods.
// If valueRequired is true, keep only those where Value is non-empty.
func normalizeCredentials(creds []MsgAccCred, valueRequired bool) []MsgAccCred {
	if len(creds) == 0 {
		return nil
	}

	index := make(map[string]*MsgAccCred)
	for _, c := range creds {
		if _, ok := globals.validators[c.Method]; ok && (!valueRequired || c.Value != "") {
			index[c.Method] = &c
		}
	}
	creds = make([]MsgAccCred, 0, len(index))
	for _, c := range index {
		creds = append(creds, *index[c.Method])
	}
	return creds
}

// Get a string slice with methods of credentials.
func credentialMethods(creds []MsgAccCred) []string {
	var out []string
	for _, c := range creds {
		out = append(out, c.Method)
	}
	return out
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

func decodeStoreError(err error, id string, timestamp time.Time, params map[string]interface{}) *ServerComMessage {

	var errmsg *ServerComMessage

	if err == nil {
		errmsg = NoErr(id, "", timestamp)
	}

	if storeErr, ok := err.(types.StoreError); !ok {
		errmsg = ErrUnknown(id, "", timestamp)
	} else {
		switch storeErr {
		case types.ErrInternal:
			errmsg = ErrUnknown(id, "", timestamp)
		case types.ErrMalformed:
			errmsg = ErrMalformed(id, "", timestamp)
		case types.ErrFailed:
			errmsg = ErrAuthFailed(id, "", timestamp)
		case types.ErrDuplicate:
			errmsg = ErrDuplicateCredential(id, "", timestamp)
		case types.ErrUnsupported:
			errmsg = ErrNotImplemented(id, "", timestamp)
		case types.ErrExpired:
			errmsg = ErrAuthFailed(id, "", timestamp)
		case types.ErrPolicy:
			errmsg = ErrPolicy(id, "", timestamp)
		case types.ErrCredentials:
			errmsg = InfoValidateCredentials(id, timestamp)
		case types.ErrNotFound:
			errmsg = ErrNotFound(id, "", timestamp)
		default:
			errmsg = ErrUnknown(id, "", timestamp)
		}
	}
	errmsg.Ctrl.Params = params

	return errmsg
}

// Helper function to select access mode for the given auth level
func selectAccessMode(authLvl auth.Level, anonMode, authMode, rootMode types.AccessMode) types.AccessMode {
	switch authLvl {
	case auth.LevelNone:
		return types.ModeNone
	case auth.LevelAnon:
		return anonMode
	case auth.LevelAuth:
		return authMode
	case auth.LevelRoot:
		return rootMode
	default:
		return types.ModeNone
	}
}

// Get default modeWant for the given topic category
func getDefaultAccess(cat types.TopicCat, auth bool) types.AccessMode {
	if !auth {
		return types.ModeNone
	}

	switch cat {
	case types.TopicCatP2P:
		return types.ModeCP2P
	case types.TopicCatFnd:
		return types.ModeNone
	case types.TopicCatGrp:
		return types.ModeCPublic
	case types.TopicCatMe:
		return types.ModeCSelf
	default:
		panic("Unknown topic category")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
