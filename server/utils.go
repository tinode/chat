// Generic data manipulation utilities.

package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
		if r.HiId <= r.LowId+1 {
			// High end is exclusive, i.e. range 1..2 is equivalent to 1.
			out = append(out, types.Range{Low: r.LowId})
		} else {
			out = append(out, types.Range{Low: r.LowId, Hi: r.HiId})
		}
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

	// Remove short, invalid tags and de-dupe keeping the order. It may result in fewer tags than could have
	// been if length were enforced later, but that's client's fault.
	var prev string
	var dst []string
	for _, curr := range src {
		if isNullValue(curr) {
			// Return non-nil empty array
			return make([]string, 0, 1)
		}

		// Unicode handling
		ucurr := []rune(curr)

		// Make sure the tag starts with a letter or a number.
		if !unicode.IsLetter(ucurr[0]) && !unicode.IsDigit(ucurr[0]) {
			continue
		}

		// Enforce length in characters, not in bytes.
		if len(ucurr) < minTagLength || len(ucurr) > maxTagLength || curr == prev {
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

// restrictedTagsEqual checks if two sets of tags contain the same set of restricted tags:
// true - same, false - different.
func restrictedTagsEqual(oldTags, newTags []string, namespaces map[string]bool) bool {
	rold := filterRestrictedTags(oldTags, namespaces)
	rnew := filterRestrictedTags(newTags, namespaces)

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
	for i := range creds {
		c := &creds[i]
		if _, ok := globals.validators[c.Method]; ok && (!valueRequired || c.Value != "") {
			index[c.Method] = c
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
	for i := range creds {
		out = append(out, creds[i].Method)
	}
	return out
}

// Take a slice of tags, return a slice of restricted namespace tags contained in the input.
// Tags to filter, restricted namespaces to filter.
func filterRestrictedTags(tags []string, namespaces map[string]bool) []string {
	var out []string
	if len(namespaces) > 0 && len(tags) > 0 {
		for _, s := range tags {
			parts := tagPrefixRegexp.FindStringSubmatch(s)

			if len(parts) < 2 {
				continue
			}

			if namespaces[parts[1]] {
				out = append(out, s)
			}
		}
	}
	return out
}

// Takes MsgClientGet query parameters, returns database query parameters
func msgOpts2storeOpts(req *MsgGetOpts) *types.QueryOpt {
	var opts *types.QueryOpt
	if req != nil {
		opts = &types.QueryOpt{
			User:            types.ParseUserId(req.User),
			Topic:           req.Topic,
			IfModifiedSince: req.IfModifiedSince,
			Limit:           req.Limit,
			Since:           req.SinceId,
			Before:          req.BeforeId,
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

func decodeStoreError(err error, id, topic string, timestamp time.Time,
	params map[string]interface{}) *ServerComMessage {

	var errmsg *ServerComMessage

	if err == nil {
		errmsg = NoErr(id, topic, timestamp)
	} else if storeErr, ok := err.(types.StoreError); !ok {
		errmsg = ErrUnknown(id, topic, timestamp)
	} else {
		switch storeErr {
		case types.ErrInternal:
			errmsg = ErrUnknown(id, topic, timestamp)
		case types.ErrMalformed:
			errmsg = ErrMalformed(id, topic, timestamp)
		case types.ErrFailed:
			errmsg = ErrAuthFailed(id, topic, timestamp)
		case types.ErrPermissionDenied:
			errmsg = ErrPermissionDenied(id, topic, timestamp)
		case types.ErrDuplicate:
			errmsg = ErrDuplicateCredential(id, topic, timestamp)
		case types.ErrUnsupported:
			errmsg = ErrNotImplemented(id, topic, timestamp)
		case types.ErrExpired:
			errmsg = ErrAuthFailed(id, topic, timestamp)
		case types.ErrPolicy:
			errmsg = ErrPolicy(id, topic, timestamp)
		case types.ErrCredentials:
			errmsg = InfoValidateCredentials(id, timestamp)
		case types.ErrNotFound:
			errmsg = ErrNotFound(id, topic, timestamp)
		default:
			errmsg = ErrUnknown(id, topic, timestamp)
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
func getDefaultAccess(cat types.TopicCat, authUser bool) types.AccessMode {
	if !authUser {
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

// Parse topic access parameters
func parseTopicAccess(acs *MsgDefaultAcsMode, defAuth, defAnon types.AccessMode) (authMode, anonMode types.AccessMode,
	err error) {

	authMode, anonMode = defAuth, defAnon

	if acs.Auth != "" {
		err = authMode.UnmarshalText([]byte(acs.Auth))
	}
	if acs.Anon != "" {
		err = anonMode.UnmarshalText([]byte(acs.Anon))
	}

	return
}

// Parses version of format 0.13.xx or 0.13-xx or 0.13
// The major and minor parts must be valid, the last part is ignored if missing or unparceable.
func parseVersion(vers string) int {
	var major, minor, trailer int
	var err error

	dot := strings.Index(vers, ".")
	if dot >= 0 {
		major, err = strconv.Atoi(vers[:dot])
	} else {
		major, err = strconv.Atoi(vers)
	}
	if err != nil {
		return 0
	}

	dot2 := strings.IndexAny(vers[dot+1:], ".-")
	if dot2 > 0 {
		minor, err = strconv.Atoi(vers[dot+1 : dot2])
		// Ignoring the error here
		trailer, _ = strconv.Atoi(vers[dot2+1:])
	} else {
		minor, err = strconv.Atoi(vers[dot+1:])
	}
	if err != nil {
		return 0
	}

	if major < 0 || minor < 0 || trailer < 0 || minor >= 0xff || trailer >= 0xff {
		return 0
	}

	return (major << 16) | (minor << 8) | trailer
}

func versionToString(vers int) string {
	str := strconv.Itoa(vers>>16) + "." + strconv.Itoa((vers>>8)&0xff)
	if vers&0xff != 0 {
		str += "-" + strconv.Itoa(vers&0xff)
	}
	return str
}

// Parser for search queries. Parameters: Fnd.Private, Fnd.Tags. The query may contain non-ASCII
// characters, i.e. length of string in bytes != length of string in runes.
// Returns AND tags (all must be present in every result), OR tags (one or more present), error.
func parseSearchQuery(query string) ([]string, []string, error) {
	type token struct {
		val string
		op  string
	}
	type context struct {
		val   string
		start int
		end   int
	}
	var ctx context
	var out []token
	query = strings.TrimSpace(query)
	for i, w := 0, 0; i < len(query); i += w {
		var r rune
		var newctx string
		r, w = utf8.DecodeRuneInString(query[i:])
		if r == '"' {
			newctx = "quo"
		} else if ctx.val != "quo" {
			if r == ' ' || r == '\t' {
				newctx = "and"
			} else if r == ',' {
				newctx = "or"
			} else if i+w == len(query) {
				newctx = "end"
				ctx.end = i + w
			}
		}

		if newctx == "quo" {
			if ctx.val == "quo" {
				ctx.val = ""
				ctx.end = i
			} else {
				ctx.val = "quo"
				ctx.start = i + w
			}
		} else if ctx.val == "or" || ctx.val == "and" {
			ctx.end = 0
			if newctx == "" {
				if len(out) == 0 {
					return nil, nil, errors.New("operator out of place " + ctx.val)
				}
				out[len(out)-1].op = ctx.val
				ctx.val = ""
				ctx.start = i

			} else if ctx.val == "or" && newctx == "or" {
				return nil, nil, errors.New("invalid operator sequence " + ctx.val)
			} else if newctx == "or" {
				// Switch context from "and" to "or", i.e. the case like ' ,' -> ','
				ctx.val = "or"
			}
			// Do nothing for cases 'and and' -> 'and', 'or and' -> 'or'.
		} else if ctx.val == "" && newctx != "" {
			end := ctx.end
			if end == 0 {
				end = i
			}
			out = append(out, token{val: query[ctx.start:end], op: newctx})
			ctx.val = newctx
			ctx.start = i
		}
	}

	if ctx.val != "" && ctx.val != "end" {
		return nil, nil, errors.New("unexpected terminal context '" + ctx.val + "'")
	}

	xlen := len(out)
	if xlen == 0 {
		return nil, nil, nil
	}

	if xlen == 1 {
		out[xlen-1].op = "and"
	} else {
		out[xlen-1].op = out[xlen-2].op
	}

	var and, or []string
	for _, t := range out {
		switch t.op {
		case "and":
			and = append(and, t.val)
		case "or":
			or = append(or, t.val)
		default:
			panic("invalid operation ='" + t.op + "', val='" + t.val + "'")
		}
	}
	return and, or, nil
}

// Returns > 0 if v1 > v2; zero if equal; < 0 if v1 < v2
// Only Major and Minor parts are compared, the trailer is ignored.
func versionCompare(v1, v2 int) int {
	return (v1 >> 8) - (v2 >> 8)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Truncate string if it's too long. Used in logging.
func truncateStringIfTooLong(s string) string {
	if len(s) <= 1024 {
		return s
	}

	return s[:1024] + "..."
}

// Convert relative filepath to absolute.
func toAbsolutePath(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(base, path))
}
