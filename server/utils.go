// Generic data manipulation utilities.

package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
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

	"golang.org/x/crypto/acme/autocert"
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
func normalizeTags(src []string) types.StringSlice {
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

	return types.StringSlice(dst)
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

	if params != nil {
		errmsg.Ctrl.Params = params
	}

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

// Parses version in the following formats:
//  1.2 | 1.2abc | 1.2.3 | 1.2.3abc
// The major and minor parts must be valid, the trailer is ignored if missing or unparceable.
func parseVersion(vers string) int {
	var major, minor, trailer int
	var err error

	dot := strings.Index(vers, ".")
	if dot < 0 {
		major, err = strconv.Atoi(vers)
		if err != nil || major > 0x1fff || major < 0 {
			return 0
		}
		return major << 16
	}

	major, err = strconv.Atoi(vers[:dot])
	if err != nil {
		return 0
	}

	vers = vers[dot+1:]
	dot2 := strings.IndexFunc(vers, func(r rune) bool {
		return !unicode.IsDigit(r)
	})

	if dot2 > 0 {
		minor, err = strconv.Atoi(vers[:dot2])
		// Ignoring the error here
		trailer, _ = strconv.Atoi(vers[dot2+1:])
	} else if len(vers) > 0 {
		minor, err = strconv.Atoi(vers)
	}
	if err != nil {
		return 0
	}

	if major < 0 || minor < 0 || trailer < 0 || major > 0x1fff || minor >= 0xff || trailer >= 0xff {
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

// Parser for search queries. The query may contain non-ASCII
// characters, i.e. length of string in bytes != length of string in runes.
// Returns AND tags (all must be present in every result), OR tags (one or more present), error.
func parseSearchQuery(query string) ([]string, []string, error) {
	const (
		NONE = iota
		QUO
		AND
		OR
		END
		ORD
	)
	type token struct {
		op  int
		val string
	}
	type context struct {
		// Pre-token operand
		preOp int
		// Post-token operand
		postOp int
		// Inside quoted string
		quo bool
		// Current token is a quoted string
		unquote bool
		// Start of the current token
		start int
		// End of the current token
		end int
	}
	var ctx = context{preOp: AND}
	var out []token
	var prev int
	query = strings.TrimSpace(query)
	// Split query into tokens.
	for i, w, pos := 0, 0, 0; prev != END; i, pos = i+w, pos+1 {
		//
		var emit bool

		// Lexer: get next rune.
		var r rune
		curr := ORD
		r, w = utf8.DecodeRuneInString(query[i:])
		switch {
		case w == 0:
			curr = END
		case r == '"':
			curr = QUO
		case !ctx.quo:
			if r == ' ' || r == '\t' {
				curr = AND
			} else if r == ',' {
				curr = OR
			}
		}

		if curr == QUO {
			if ctx.quo {
				// End of the quoted string. Close the quote.
				ctx.quo = false
			} else {
				if prev == ORD {
					// Reject strings like a"b
					return nil, nil, fmt.Errorf("missing operator at or near %d", pos)
				}
				// Start of the quoted string. Open the quote.
				ctx.quo = true
				ctx.unquote = true
			}
			curr = ORD
		}

		// Parser: process the current lexem in context.
		switch curr {
		case OR:
			if ctx.postOp == OR {
				// More than one comma: ' , ,,'
				return nil, nil, fmt.Errorf("invalid operator sequence at or near %d", pos)
			}
			// Ensure context is not "and", i.e. the case like ' ,' -> ','
			ctx.postOp = OR
			if prev == ORD {
				// Close the current token.
				ctx.end = i
			}
		case AND:
			if prev == ORD {
				// Close the current token.
				ctx.end = i
				ctx.postOp = AND
			} else if ctx.postOp != OR {
				// "and" does not change the "or" context.
				ctx.postOp = AND
			}
		case ORD:
			if prev == OR || prev == AND {
				// Ordinary character after a comma or a space: ' a' or ',a'.
				// Emit without changing the operation.
				emit = true
			}
		case END:
			if prev == ORD {
				// Close the current token.
				ctx.end = i
			}
			emit = true
		}

		if emit {
			if ctx.quo {
				return nil, nil, fmt.Errorf("unterminated quoted string at or near %d", pos)
			}

			// Emit the new token.
			op := ctx.preOp
			if ctx.postOp == OR {
				op = OR
			}
			start, end := ctx.start, ctx.end
			if ctx.unquote {
				start++
				end--
			}
			// Add token if non-empty.
			if start < end {
				out = append(out, token{val: query[start:end], op: op})
			}
			ctx.start = i
			ctx.preOp = ctx.postOp
			ctx.postOp = NONE
			ctx.unquote = false
		}

		prev = curr
	}

	if len(out) == 0 {
		return nil, nil, nil
	}

	// Convert tokens to two string slices.
	var and, or []string
	for _, t := range out {
		switch t.op {
		case AND:
			and = append(and, t.val)
		case OR:
			or = append(or, t.val)
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

// Detect platform from the UserAgent string.
func platformFromUA(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "reactnative"):
		switch {
		case strings.Contains(ua, "iphone"),
			strings.Contains(ua, "ipad"):
			return "ios"
		case strings.Contains(ua, "android"):
			return "android"
		}
		return ""
	case strings.Contains(ua, "tinodejs"):
		return "web"
	case strings.Contains(ua, "tindroid"):
		return "android"
	}
	return ""
}

func parseTLSConfig(tlsEnabled bool, jsconfig json.RawMessage) (*tls.Config, error) {
	type tlsAutocertConfig struct {
		// Domains to support by autocert
		Domains []string `json:"domains"`
		// Name of directory where auto-certificates are cached, e.g. /etc/letsencrypt/live/your-domain-here
		CertCache string `json:"cache"`
		// Contact email for letsencrypt
		Email string `json:"email"`
	}

	type tlsConfig struct {
		// Flag enabling TLS
		Enabled bool `json:"enabled"`
		// Listen for connections on this address:port and redirect them to HTTPS port.
		RedirectHTTP string `json:"http_redirect"`
		// Enable Strict-Transport-Security by setting max_age > 0
		StrictMaxAge int `json:"strict_max_age"`
		// ACME autocert config, e.g. letsencrypt.org
		Autocert *tlsAutocertConfig `json:"autocert"`
		// If Autocert is not defined, provide file names of static certificate and key
		CertFile string `json:"cert_file"`
		KeyFile  string `json:"key_file"`
	}

	var config tlsConfig

	if jsconfig != nil {
		if err := json.Unmarshal(jsconfig, &config); err != nil {
			return nil, errors.New("http: failed to parse tls_config: " + err.Error() + "(" + string(jsconfig) + ")")
		}
	}

	if !tlsEnabled && !config.Enabled {
		return nil, nil
	}

	if config.StrictMaxAge > 0 {
		globals.tlsStrictMaxAge = strconv.Itoa(config.StrictMaxAge)
	}

	globals.tlsRedirectHTTP = config.RedirectHTTP

	// If autocert is provided, use it.
	if config.Autocert != nil {
		certManager := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(config.Autocert.Domains...),
			Cache:      autocert.DirCache(config.Autocert.CertCache),
			Email:      config.Autocert.Email,
		}
		return certManager.TLSConfig(), nil
	}

	// Otherwise try to use static keys.
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}
