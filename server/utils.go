// Generic data manipulation utilities.

package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/crypto/acme/autocert"
)

// Tag with prefix:
// * prefix starts with an ASCII letter, contains ASCII letters, numbers, from 2 to 16 chars
// * tag body may contain Unicode letters and numbres, as well as the following symbols: +-.!?#@_
// Tag body can be up to maxTagLength (96) chars long.
var prefixedTagRegexp = regexp.MustCompile(`^([a-z]\w{1,15}):[-_+.!?#@\pL\pN]{1,96}$`)

// Generic tag: the same restrictions as tag body.
var tagRegexp = regexp.MustCompile(`^[-_+.!?#@\pL\pN]{1,96}$`)

// Token suitable as a login: 3-16 chars, starts with a Unicode letter (class L) and contains Unicode letters (L),
// numbers (N) and underscore.
var basicLoginName = regexp.MustCompile(`^\pL[_\pL\pN]{2,15}$`)

const nullValue = "\u2421"

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

// Process credentials for correctness: remove duplicate and unknown methods.
// In case of duplicate methods only the first one satisfying valueRequired is kept.
// If valueRequired is true, keep only those where Value is non-empty.
func normalizeCredentials(creds []MsgCredClient, valueRequired bool) []MsgCredClient {
	if len(creds) == 0 {
		return nil
	}

	index := make(map[string]*MsgCredClient)
	for i := range creds {
		c := &creds[i]
		if _, ok := globals.validators[c.Method]; ok && (!valueRequired || c.Value != "") {
			index[c.Method] = c
		}
	}
	creds = make([]MsgCredClient, 0, len(index))
	for _, c := range index {
		creds = append(creds, *index[c.Method])
	}
	return creds
}

// Get a string slice with methods of credentials.
func credentialMethods(creds []MsgCredClient) []string {
	var out []string
	for i := range creds {
		out = append(out, creds[i].Method)
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
	if str, ok := i.(string); ok {
		return str == nullValue
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
		case types.ErrUserNotFound:
			errmsg = ErrUserNotFound(id, topic, timestamp)
		case types.ErrTopicNotFound:
			errmsg = ErrTopicNotFound(id, topic, timestamp)
		case types.ErrNotFound:
			errmsg = ErrNotFound(id, topic, timestamp)
		case types.ErrInvalidResponse:
			errmsg = ErrInvalidResponse(id, topic, timestamp)
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

// Parse one component of a semantic version string.
func parseVersionPart(vers string) int {
	end := strings.IndexFunc(vers, func(r rune) bool {
		return !unicode.IsDigit(r)
	})

	t := 0
	var err error
	if end > 0 {
		t, err = strconv.Atoi(vers[:end])
	} else if len(vers) > 0 {
		t, err = strconv.Atoi(vers)
	}
	if err != nil || t > 0x1fff || t <= 0 {
		return 0
	}
	return t
}

// Parses semantic version string in the following formats:
//  1.2, 1.2abc, 1.2.3, 1.2.3-abc, v0.12.34-rc5
// Unparceable values are replaced with zeros.
func parseVersion(vers string) int {
	var major, minor, patch int
	// Remove optional "v" prefix.
	if strings.HasPrefix(vers, "v") {
		vers = vers[1:]
	}
	// We can handle 3 parts only.
	parts := strings.SplitN(vers, ".", 3)
	count := len(parts)
	if count > 0 {
		major = parseVersionPart(parts[0])
		if count > 1 {
			minor = parseVersionPart(parts[1])
			if count > 2 {
				patch = parseVersionPart(parts[2])
			}
		}
	}

	return (major << 16) | (minor << 8) | patch
}

// Version as a base-10 number. Used by monitoring.
func base10Version(hex int) int64 {
	major := hex >> 16 & 0xFF
	minor := hex >> 8 & 0xFF
	trailer := hex & 0xFF
	return int64(major*10000 + minor*100 + trailer)
}

func versionToString(vers int) string {
	str := strconv.Itoa(vers>>16) + "." + strconv.Itoa((vers>>8)&0xff)
	if vers&0xff != 0 {
		str += "-" + strconv.Itoa(vers&0xff)
	}
	return str
}

// Tag handling

// Take a slice of tags, return a slice of restricted namespace tags contained in the input.
// Tags to filter, restricted namespaces to filter.
func filterRestrictedTags(tags []string, namespaces map[string]bool) []string {
	var out []string
	if len(namespaces) == 0 {
		return out
	}

	for _, s := range tags {
		parts := prefixedTagRegexp.FindStringSubmatch(s)

		if len(parts) < 2 {
			continue
		}

		if namespaces[parts[1]] {
			out = append(out, s)
		}
	}

	return out
}

// rewriteToken attempts to match the original token against the email, telephone number and optionally login patterns.
// The tag is expected to be converted to lowercase.
// On success, it prepends the token with the corresponding prefix. It returns an empty string if the tag is invalid.
// TODO: better handling of countryCode:
// 1. As provided by the client (e.g. as specified or inferred from client's phone number or location).
// 2. Use value from the .conf file.
// 3. Fallback to US as a last resort.
func rewriteTag(orig, countryCode string, withLogin bool) string {
	// Check if the tag already has a prefix e.g. basic:alice.
	if prefixedTagRegexp.MatchString(orig) {
		return orig
	}

	// Is it email?
	if addr, err := mail.ParseAddress(orig); err == nil {
		if len([]rune(addr.Address)) < maxTagLength && addr.Address == orig {
			return "email:" + orig
		}
		return ""
	}

	if num, err := phonenumbers.Parse(orig, countryCode); err == nil {
		// It's a phone number. Not checking the length because phone numbers cannot be that long.
		return "tel:" + phonenumbers.Format(num, phonenumbers.E164)
	}

	// Does it look like a username/login?
	// TODO: use configured authenticators to check if orig is a valid user name.
	if withLogin && basicLoginName.MatchString(orig) {
		return "basic:" + orig
	}

	if tagRegexp.MatchString(orig) {
		return orig
	}
	return ""
}

// Parser for search queries. The query may contain non-ASCII characters,
// i.e. length of string in bytes != length of string in runes.
// Returns
// * required tags: AND of ORs of tags (at least one of each subset must be present in every result),
// * optional tags
// * error.
func parseSearchQuery(query, countryCode string, withLogin bool) ([][]string, []string, error) {
	const (
		NONE = iota
		QUO
		AND
		OR
		END
		ORD
	)
	type token struct {
		op           int
		val          string
		rewrittenVal string
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
				original := strings.ToLower(query[start:end])
				rewritten := rewriteTag(original, countryCode, withLogin)
				// The 'rewritten' equals to "" means the token is invalid.
				if rewritten != "" {
					t := token{val: original, op: op}
					if rewritten != original {
						t.rewrittenVal = rewritten
					}
					out = append(out, t)
				}
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
	var and [][]string
	var or []string
	for _, t := range out {
		switch t.op {
		case AND:
			var terms []string
			terms = append(terms, t.val)
			if len(t.rewrittenVal) > 0 {
				terms = append(terms, t.rewrittenVal)
			}
			and = append(and, terms)
		case OR:
			or = append(or, t.val)
			if len(t.rewrittenVal) > 0 {
				or = append(or, t.rewrittenVal)
			}
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
	case strings.Contains(ua, "tinodios"):
		return "ios"
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

// Merge source interface{} into destination interface.
// If values are maps,deep-merge them. Otherwise shallow-copy.
// Returns dst, true if the dst value was changed.
func mergeInterfaces(dst, src interface{}) (interface{}, bool) {
	var changed bool

	if src == nil {
		return dst, changed
	}

	vsrc := reflect.ValueOf(src)
	switch vsrc.Kind() {
	case reflect.Map:
		if xsrc, ok := src.(map[string]interface{}); ok {
			xdst, _ := dst.(map[string]interface{})
			dst, changed = mergeMaps(xdst, xsrc)
		} else {
			changed = true
			dst = src
		}
	case reflect.String:
		if vsrc.String() == nullValue {
			changed = dst != nil
			dst = nil
		} else {
			changed = true
			dst = src
		}
	default:
		changed = true
		dst = src
	}
	return dst, changed
}

// Deep copy maps.
func mergeMaps(dst, src map[string]interface{}) (map[string]interface{}, bool) {
	var changed bool

	if len(src) == 0 {
		return dst, changed
	}

	if dst == nil {
		dst = make(map[string]interface{})
	}

	for key, val := range src {
		xval := reflect.ValueOf(val)
		switch xval.Kind() {
		case reflect.Map:
			if xsrc, _ := val.(map[string]interface{}); xsrc != nil {
				// Deep-copy map[string]interface{}
				xdst, _ := dst[key].(map[string]interface{})
				var lchange bool
				dst[key], lchange = mergeMaps(xdst, xsrc)
				changed = changed || lchange
			} else if val != nil {
				// The map is shallow-copied if it's not of the type map[string]interface{}
				dst[key] = val
				changed = true
			}
		case reflect.String:
			changed = true
			if xval.String() == nullValue {
				delete(dst, key)
			} else if val != nil {
				dst[key] = val
			}
		default:
			if val != nil {
				dst[key] = val
				changed = true
			}
		}
	}

	return dst, changed
}

// netListener creates net.Listener for tcp and unix domains:
// if addr is is in the form "unix:/run/tinode.sock" it's a unix socket, otherwise TCP host:port.
func netListener(addr string) (net.Listener, error) {
	addrParts := strings.SplitN(addr, ":", 2)
	if len(addrParts) == 2 && addrParts[0] == "unix" {
		return net.Listen("unix", addrParts[1])
	}
	return net.Listen("tcp", addr)
}

// Check if specified address is a unix socket like "unix:/run/tinode.sock".
func isUnixAddr(addr string) bool {
	addrParts := strings.SplitN(addr, ":", 2)
	return len(addrParts) == 2 && addrParts[0] == "unix"
}
