// Generic data manipulation utilities.

package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"maps"

	"golang.org/x/crypto/acme/autocert"
)

// Tag with prefix:
// * prefix starts with an ASCII letter, contains ASCII letters, numbers, from 2 to 16 chars
// * tag body may contain Unicode letters and numbers, as well as the following symbols: +-.!?#@_
// Tag body can be up to maxTagLength (96) chars long.
var prefixedTagRegexp = regexp.MustCompile(`^([a-z]\w{1,15}):([-_+.!?#@\pL\pN]{1,96})$`)

// Generic tag: the same restrictions as tag body.
var tagRegexp = regexp.MustCompile(`^[-_+.!?#@\pL\pN]{1,96}$`)

const nullValue = "\u2421"

// Convert database ranges into wire protocol ranges.
func rangeDeserialize(in []types.Range) []MsgRange {
	if len(in) == 0 {
		return nil
	}

	out := make([]MsgRange, 0, len(in))
	for _, r := range in {
		out = append(out, MsgRange{LowId: r.Low, HiId: r.Hi})
	}

	return out
}

// Convert wire protocol ranges into database ranges.
func rangeSerialize(in []MsgRange) []types.Range {
	if len(in) == 0 {
		return nil
	}

	out := make([]types.Range, 0, len(in))
	for _, r := range in {
		out = append(out, types.Range{Low: r.LowId, Hi: r.HiId})
	}

	return out
}

// stringSliceDelta extracts the slices of added and removed strings from two slices:
//
//	added :=  newSlice - (oldSlice & newSlice) -- present in new but missing in old
//	removed := oldSlice - (oldSlice & newSlice) -- present in old but missing in new
//	intersection := oldSlice & newSlice -- present in both old and new
func stringSliceDelta(rold, rnew []string) (added, removed, intersection []string) {
	if len(rold) == 0 && len(rnew) == 0 {
		return nil, nil, nil
	}
	if len(rold) == 0 {
		return rnew, nil, nil
	}
	if len(rnew) == 0 {
		return nil, rold, nil
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
			intersection = append(intersection, rold[o])
			if o < lold {
				o++
			}
			if n < lnew {
				n++
			}
		}
	}
	return added, removed, intersection
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
	out := make([]string, len(creds))
	for i := range creds {
		out[i] = creds[i].Method
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
			IdRanges:        rangeSerialize(req.IdRanges),
		}
	}
	return opts
}

// Check if the interface contains a string with a single Unicode Del control character.
func isNullValue(i any) bool {
	if str, ok := i.(string); ok {
		return str == nullValue
	}
	return false
}

func decodeStoreError(err error, id string, ts time.Time, params map[string]any) *ServerComMessage {
	return decodeStoreErrorExplicitTs(err, id, "", ts, ts, params)
}

func decodeStoreErrorExplicitTs(err error, id, topic string, serverTs, incomingReqTs time.Time,
	params map[string]any) *ServerComMessage {

	var errmsg *ServerComMessage

	if err == nil {
		errmsg = NoErrExplicitTs(id, topic, serverTs, incomingReqTs)
	} else if storeErr, ok := err.(types.StoreError); !ok {
		errmsg = ErrUnknownExplicitTs(id, topic, serverTs, incomingReqTs)
	} else {
		switch storeErr {
		case types.ErrInternal:
			errmsg = ErrUnknownExplicitTs(id, topic, serverTs, incomingReqTs)
		case types.ErrMalformed:
			errmsg = ErrMalformedExplicitTs(id, topic, serverTs, incomingReqTs)
		case types.ErrFailed:
			errmsg = ErrAuthFailed(id, topic, serverTs, incomingReqTs)
		case types.ErrPermissionDenied:
			errmsg = ErrPermissionDeniedExplicitTs(id, topic, serverTs, incomingReqTs)
		case types.ErrDuplicate:
			errmsg = ErrDuplicateCredential(id, topic, serverTs, incomingReqTs)
		case types.ErrUnsupported:
			errmsg = ErrNotImplemented(id, topic, serverTs, incomingReqTs)
		case types.ErrExpired:
			errmsg = ErrAuthFailed(id, topic, serverTs, incomingReqTs)
		case types.ErrPolicy:
			errmsg = ErrPolicyExplicitTs(id, topic, serverTs, incomingReqTs)
		case types.ErrCredentials:
			errmsg = InfoValidateCredentialsExplicitTs(id, serverTs, incomingReqTs)
		case types.ErrUserNotFound:
			errmsg = ErrUserNotFound(id, topic, serverTs, incomingReqTs)
		case types.ErrTopicNotFound:
			errmsg = ErrTopicNotFound(id, topic, serverTs, incomingReqTs)
		case types.ErrNotFound:
			errmsg = ErrNotFoundExplicitTs(id, topic, serverTs, incomingReqTs)
		case types.ErrInvalidResponse:
			errmsg = ErrInvalidResponse(id, topic, serverTs, incomingReqTs)
		case types.ErrRedirected:
			errmsg = InfoUseOther(id, topic, params["topic"].(string), serverTs, incomingReqTs)
		default:
			errmsg = ErrUnknownExplicitTs(id, topic, serverTs, incomingReqTs)
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
func getDefaultAccess(cat types.TopicCat, authUser, isChan bool) types.AccessMode {
	if !authUser {
		return types.ModeNone
	}

	switch cat {
	case types.TopicCatP2P:
		return globals.typesModeCP2P
	case types.TopicCatFnd:
		return types.ModeNone
	case types.TopicCatGrp:
		if isChan {
			return types.ModeCChnWriter
		}
		return types.ModeCPublic
	case types.TopicCatMe:
		return types.ModeCMeFnd
	case types.TopicCatSlf:
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
//
//	1.2, 1.2abc, 1.2.3, 1.2.3-abc, v0.12.34-rc5
//
// Unparceable values are replaced with zeros.
func parseVersion(vers string) int {
	var major, minor, patch int
	// Maybe remove the optional "v" prefix.
	vers = strings.TrimPrefix(vers, "v")

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

// filterTags takes a slice of tags and a map of namespaces, return a slice of namespace tags
// contained in the input.
// params: Tags to filter, namespaces to use as the filter.
func filterTags(tags []string, namespaces map[string]bool) []string {
	var out []string
	if len(namespaces) == 0 {
		return out
	}

	for _, s := range tags {
		parts := prefixedTagRegexp.FindStringSubmatch(s)

		if len(parts) < 2 {
			continue
		}

		// [1] is the prefix. [0] is the whole tag.
		if namespaces[parts[1]] {
			out = append(out, s)
		}
	}

	return out
}

// rewriteTag attempts to match the original token against the email and telephone number.
// The tag is expected to be in lowercase.
// On success, it returns a slice with the original tag and the tag with the corresponding prefix. It returns an
// empty slice if the tag is invalid.
// TODO: consider inferring country code from user location.
func rewriteTag(orig, countryCode string) []string {
	// Check if the tag already has a prefix e.g. basic:alice.
	if prefixedTagRegexp.MatchString(orig) {
		return []string{orig}
	}

	// Check if token can be rewritten by any of the validators
	param := map[string]any{"countryCode": countryCode}
	for name, conf := range globals.validators {
		if conf.addToTags {
			val := store.Store.GetValidator(name)
			if tag, _ := val.PreCheck(orig, param); tag != "" {
				return []string{orig, tag}
			}
		}
	}

	if tagRegexp.MatchString(orig) {
		return []string{orig}
	}

	// invalid generic tag

	return nil
}

// rewriteTagSlice calls rewriteTag for each slice member and return a new slice with original and converted values.
func rewriteTagSlice(tags []string, countryCode string) []string {
	var result []string
	for _, tag := range tags {
		rewritten := rewriteTag(tag, countryCode)
		if len(rewritten) != 0 {
			result = append(result, rewritten...)
		}
	}
	return result
}

// restrictedTagsEqual checks if two sets of tags contain the same set of restricted tags:
// true - same, false - different.
func restrictedTagsEqual(oldTags, newTags []string, namespaces map[string]bool) bool {
	rold := filterTags(oldTags, namespaces)
	rnew := filterTags(newTags, namespaces)

	if len(rold) != len(rnew) {
		return false
	}

	sort.Strings(rold)
	sort.Strings(rnew)

	// Match old tags against the new tags.
	for i := range rnew {
		if rold[i] != rnew[i] {
			return false
		}
	}

	return true
}

// Trim whitespace, remove short/empty tags and duplicates, convert to lowercase, ensure
// the number of tags does not exceed the maximum.
func normalizeTags(src []string, maxTags int) types.StringSlice {
	if src == nil {
		return nil
	}

	// Make sure the number of tags does not exceed the maximum.
	// Technically it may result in fewer tags than the maximum due to empty tags and
	// duplicates, but that's user's fault.
	if len(src) > maxTags {
		src = src[:maxTags]
	}

	// Trim whitespace and force to lowercase.
	for i := range src {
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

		// Enforce length in characters, not in bytes.
		if len(ucurr) < minTagLength || len(ucurr) > maxTagLength || curr == prev {
			continue
		}

		// Make sure the tag starts with a letter or a number.
		if unicode.IsLetter(ucurr[0]) || unicode.IsDigit(ucurr[0]) {
			dst = append(dst, curr)
			prev = curr
		}
	}

	return types.StringSlice(dst)
}

func validateTag(tag string) (string, string) {
	// Check if the tag already has a prefix e.g. basic:alice.
	if parts := prefixedTagRegexp.FindStringSubmatch(tag); len(parts) == 3 {
		// Valid prefixed tag.
		return parts[1], parts[2]
	}

	if tagRegexp.MatchString(tag) {
		// Valid unprefixed tag (tag value only).
		return "", tag
	}

	return "", ""
}

// hasDuplicateNamespaceTags checks for duplication of unique NS tags.
// Each namespace can have only one tag. This does not prevent tags from
// being duplicate across requests, just saves an extra DB call.
func hasDuplicateNamespaceTags(src []string, uniqueNS string) bool {
	found := map[string]bool{}
	for _, tag := range src {
		parts := prefixedTagRegexp.FindStringSubmatch(tag)
		if len(parts) != 3 {
			// Invalid tag, ignored.
			continue
		}

		if uniqueNS == parts[1] && found[parts[1]] {
			return true
		}
		found[parts[1]] = true
	}
	return false
}

// Parser for search queries. The query may contain non-ASCII characters,
// i.e. length of string in bytes != length of string in runes.
// Returns
// * required tags: AND tags (at least one must be present in every result),
// * optional tags
// * error.
func parseSearchQuery(query string) ([]string, []string, error) {
	const (
		NONE = iota
		QUO  // 1
		AND  // 2
		OR   // 3
		END  // 4
		ORD  // 5
	)
	type token struct {
		op  int
		val string
	}
	type context struct {
		// Pre-token operand.
		preOp int
		// Post-token operand.
		postOp int
		// Inside quoted string.
		quo bool
		// Start of the current token.
		start int
		// End of the current token.
		end int
	}
	ctx := context{preOp: AND}
	var out []token
	var prev int
	query = strings.TrimSpace(query)
	// Split query into tokens.
	//   i - character index into the string.
	//   pos - rune index into the string.
	//   w - width of the current rune in characters.
	for i, w, pos := 0, 0, 0; prev != END; i, pos = i+w, pos+1 {
		//
		var emit bool

		// Lexer: get next rune.
		var r rune
		// Ordinary character by default.
		curr := ORD
		r, w = utf8.DecodeRuneInString(query[i:])
		switch {
		case w == 0:
			// Width zero: end of the string.
			curr = END
		case r == '"':
			// Quote opening or closing.
			curr = QUO
		case !ctx.quo:
			// Not inside quoted string, test for control characters.
			if r == ' ' || r == '\t' {
				// Tab or space.
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
			}
			// Treat quoted string as ordinary.
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
			if ctx.quo && curr == END {
				return nil, nil, fmt.Errorf("unterminated quoted string at or near %d %#v", pos, ctx)
			}

			// Emit the new token.
			op := ctx.preOp
			if ctx.postOp == OR {
				op = OR
			}
			start, end := ctx.start, ctx.end
			if query[start] == '"' && query[end-1] == '"' {
				start++
				end--
			}
			// Add token if non-empty.
			if start < end {
				out = append(out, token{val: strings.ToLower(query[start:end]), op: op})
			}
			ctx.start = i
			ctx.preOp, ctx.postOp = ctx.postOp, NONE
		}

		prev = curr
	}

	if len(out) == 0 {
		return nil, nil, nil
	}

	// Convert tokens to two string slices.
	var and []string
	var or []string
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
func mergeInterfaces(dst, src any) (any, bool) {
	var changed bool

	if src == nil {
		return dst, changed
	}

	vsrc := reflect.ValueOf(src)
	switch vsrc.Kind() {
	case reflect.Map:
		if xsrc, ok := src.(map[string]any); ok {
			xdst, _ := dst.(map[string]any)
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
func mergeMaps(dst, src map[string]any) (map[string]any, bool) {
	var changed bool

	if len(src) == 0 {
		return dst, changed
	}

	if dst == nil {
		dst = make(map[string]any)
	}

	for key, val := range src {
		xval := reflect.ValueOf(val)
		switch xval.Kind() {
		case reflect.Map:
			if xsrc, _ := val.(map[string]any); xsrc != nil {
				// Deep-copy map[string]any
				xdst, _ := dst[key].(map[string]any)
				var lchange bool
				dst[key], lchange = mergeMaps(xdst, xsrc)
				changed = changed || lchange
			} else if val != nil {
				// The map is shallow-copied if it's not of the type map[string]any
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

// Shallow copy of a map
func copyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	maps.Copy(dst, src)
	return dst
}

// netListener creates net.Listener for tcp and unix domains:
// if addr is in the form "unix:/run/tinode.sock" it's a unix socket, otherwise TCP host:port.
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

var privateIPBlocks []*net.IPNet

func isRoutableIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}

	if privateIPBlocks == nil {
		for _, cidr := range []string{
			"10.0.0.0/8",     // RFC1918
			"172.16.0.0/12",  // RFC1918
			"192.168.0.0/16", // RFC1918
			"fc00::/7",       // RFC4193, IPv6 unique local addr
		} {
			_, block, _ := net.ParseCIDR(cidr)
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}

	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
