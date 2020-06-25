// Package types provides data types for persisting objects in the databases.
package types

import (
	"database/sql/driver"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// StoreError satisfies Error interface but allows constant values for
// direct comparison.
type StoreError string

// Error is required by error interface.
func (s StoreError) Error() string {
	return string(s)
}

const (
	// ErrInternal means DB or other internal failure.
	ErrInternal = StoreError("internal")
	// ErrMalformed means the secret cannot be parsed or otherwise wrong.
	ErrMalformed = StoreError("malformed")
	// ErrFailed means authentication failed (wrong login or password, etc).
	ErrFailed = StoreError("failed")
	// ErrDuplicate means duplicate credential, i.e. non-unique login.
	ErrDuplicate = StoreError("duplicate value")
	// ErrUnsupported means an operation is not supported.
	ErrUnsupported = StoreError("unsupported")
	// ErrExpired means the secret has expired.
	ErrExpired = StoreError("expired")
	// ErrPolicy means policy violation, e.g. password too weak.
	ErrPolicy = StoreError("policy")
	// ErrCredentials means credentials like email or captcha must be validated.
	ErrCredentials = StoreError("credentials")
	// ErrUserNotFound means the user was not found.
	ErrUserNotFound = StoreError("user not found")
	// ErrTopicNotFound means the topic was not found.
	ErrTopicNotFound = StoreError("topic not found")
	// ErrNotFound means the object other then user or topic was not found.
	ErrNotFound = StoreError("not found")
	// ErrPermissionDenied means the operation is not permitted.
	ErrPermissionDenied = StoreError("denied")
	// ErrInvalidResponse means the client's response does not match server's expectation.
	ErrInvalidResponse = StoreError("invalid response")
)

// Uid is a database-specific record id, suitable to be used as a primary key.
type Uid uint64

// ZeroUid is a constant representing uninitialized Uid.
const ZeroUid Uid = 0

// NullValue is a Unicode DEL character which indicated that the value is being deleted.
const NullValue = "\u2421"

// Lengths of various Uid representations
const (
	uidBase64Unpadded = 11
	p2pBase64Unpadded = 22
)

// IsZero checks if Uid is uninitialized.
func (uid Uid) IsZero() bool {
	return uid == ZeroUid
}

// Compare returns 0 if uid is equal to u2, 1 if u2 is greater than uid, -1 if u2 is smaller.
func (uid Uid) Compare(u2 Uid) int {
	if uid < u2 {
		return -1
	} else if uid > u2 {
		return 1
	}
	return 0
}

// MarshalBinary converts Uid to byte slice.
func (uid Uid) MarshalBinary() ([]byte, error) {
	dst := make([]byte, 8)
	binary.LittleEndian.PutUint64(dst, uint64(uid))
	return dst, nil
}

// UnmarshalBinary reads Uid from byte slice.
func (uid *Uid) UnmarshalBinary(b []byte) error {
	if len(b) < 8 {
		return errors.New("Uid.UnmarshalBinary: invalid length")
	}
	*uid = Uid(binary.LittleEndian.Uint64(b))
	return nil
}

// UnmarshalText reads Uid from string represented as byte slice.
func (uid *Uid) UnmarshalText(src []byte) error {
	if len(src) != uidBase64Unpadded {
		return errors.New("Uid.UnmarshalText: invalid length")
	}
	dec := make([]byte, base64.URLEncoding.WithPadding(base64.NoPadding).DecodedLen(uidBase64Unpadded))
	count, err := base64.URLEncoding.WithPadding(base64.NoPadding).Decode(dec, src)
	if count < 8 {
		if err != nil {
			return errors.New("Uid.UnmarshalText: failed to decode " + err.Error())
		}
		return errors.New("Uid.UnmarshalText: failed to decode")
	}
	*uid = Uid(binary.LittleEndian.Uint64(dec))
	return nil
}

// MarshalText converts Uid to string represented as byte slice.
func (uid *Uid) MarshalText() ([]byte, error) {
	if *uid == ZeroUid {
		return []byte{}, nil
	}
	src := make([]byte, 8)
	dst := make([]byte, base64.URLEncoding.WithPadding(base64.NoPadding).EncodedLen(8))
	binary.LittleEndian.PutUint64(src, uint64(*uid))
	base64.URLEncoding.WithPadding(base64.NoPadding).Encode(dst, src)
	return dst, nil
}

// MarshalJSON converts Uid to double quoted ("ajjj") string.
func (uid *Uid) MarshalJSON() ([]byte, error) {
	dst, _ := uid.MarshalText()
	return append(append([]byte{'"'}, dst...), '"'), nil
}

// UnmarshalJSON reads Uid from a double quoted string.
func (uid *Uid) UnmarshalJSON(b []byte) error {
	size := len(b)
	if size != (uidBase64Unpadded + 2) {
		return errors.New("Uid.UnmarshalJSON: invalid length")
	} else if b[0] != '"' || b[size-1] != '"' {
		return errors.New("Uid.UnmarshalJSON: unrecognized")
	}
	return uid.UnmarshalText(b[1 : size-1])
}

// String converts Uid to base64 string.
func (uid Uid) String() string {
	buf, _ := uid.MarshalText()
	return string(buf)
}

// String32 converts Uid to lowercase base32 string (suitable for file names on Windows).
func (uid Uid) String32() string {
	data, _ := uid.MarshalBinary()
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data))
}

// ParseUid parses string NOT prefixed with anything
func ParseUid(s string) Uid {
	var uid Uid
	uid.UnmarshalText([]byte(s))
	return uid
}

// ParseUid32 parses base32-encoded string into Uid
func ParseUid32(s string) Uid {
	var uid Uid
	if data, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s); err == nil {
		uid.UnmarshalBinary(data)
	}
	return uid
}

// UserId converts Uid to string prefixed with 'usr', like usrXXXXX
func (uid Uid) UserId() string {
	return uid.PrefixId("usr")
}

// FndName generates 'fnd' topic name for the given Uid.
func (uid Uid) FndName() string {
	return uid.PrefixId("fnd")
}

// PrefixId converts Uid to string prefixed with the given prefix.
func (uid Uid) PrefixId(prefix string) string {
	if uid.IsZero() {
		return ""
	}
	return prefix + uid.String()
}

// ParseUserId parses user ID of the form "usrXXXXXX"
func ParseUserId(s string) Uid {
	var uid Uid
	if strings.HasPrefix(s, "usr") {
		(&uid).UnmarshalText([]byte(s)[3:])
	}
	return uid
}

// UidSlice is a slice of Uids sorted in ascending order.
type UidSlice []Uid

func (us UidSlice) find(uid Uid) (int, bool) {
	l := len(us)
	if l == 0 || us[0] > uid {
		return 0, false
	}
	if uid > us[l-1] {
		return l, false
	}
	idx := sort.Search(l, func(i int) bool {
		return uid <= us[i]
	})
	return idx, idx < l && us[idx] == uid
}

// Add uid to UidSlice keeping it sorted. Duplicates are ignored.
func (us *UidSlice) Add(uid Uid) bool {
	idx, found := us.find(uid)
	if found {
		return false
	}
	// Inserting without creating a temporary slice.
	*us = append(*us, ZeroUid)
	copy((*us)[idx+1:], (*us)[idx:])
	(*us)[idx] = uid
	return true
}

// Rem removes uid from UidSlice.
func (us *UidSlice) Rem(uid Uid) bool {
	idx, found := us.find(uid)
	if !found {
		return false
	}
	if idx == len(*us)-1 {
		*us = (*us)[:idx]
	} else {
		*us = append((*us)[:idx], (*us)[idx+1:]...)
	}
	return true
}

// Contains checks if the UidSlice contains the given uid
func (us UidSlice) Contains(uid Uid) bool {
	_, contains := us.find(uid)
	return contains
}

// P2PName takes two Uids and generates a P2P topic name
func (uid Uid) P2PName(u2 Uid) string {
	if !uid.IsZero() && !u2.IsZero() {
		b1, _ := uid.MarshalBinary()
		b2, _ := u2.MarshalBinary()

		if uid < u2 {
			b1 = append(b1, b2...)
		} else if uid > u2 {
			b1 = append(b2, b1...)
		} else {
			// Explicitly disable P2P with self
			return ""
		}

		return "p2p" + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b1)
	}

	return ""
}

// ParseP2P extracts uids from the name of a p2p topic.
func ParseP2P(p2p string) (uid1, uid2 Uid, err error) {
	if strings.HasPrefix(p2p, "p2p") {
		src := []byte(p2p)[3:]
		if len(src) != p2pBase64Unpadded {
			err = errors.New("ParseP2P: invalid length")
			return
		}
		dec := make([]byte, base64.URLEncoding.WithPadding(base64.NoPadding).DecodedLen(p2pBase64Unpadded))
		var count int
		count, err = base64.URLEncoding.WithPadding(base64.NoPadding).Decode(dec, src)
		if count < 16 {
			if err != nil {
				err = errors.New("ParseP2P: failed to decode " + err.Error())
			} else {
				err = errors.New("ParseP2P: invalid decoded length")
			}
			return
		}
		uid1 = Uid(binary.LittleEndian.Uint64(dec))
		uid2 = Uid(binary.LittleEndian.Uint64(dec[8:]))
	} else {
		err = errors.New("ParseP2P: missing or invalid prefix")
	}
	return
}

// ObjHeader is the header shared by all stored objects.
type ObjHeader struct {
	// using string to get around rethinkdb's problems with uint64;
	// `bson:"_id"` tag is for mongodb to use as primary key '_id'.
	Id        string `bson:"_id"`
	id        Uid
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Uid assigns Uid header field.
func (h *ObjHeader) Uid() Uid {
	if h.id.IsZero() && h.Id != "" {
		h.id.UnmarshalText([]byte(h.Id))
	}
	return h.id
}

// SetUid assigns given Uid to appropriate header fields.
func (h *ObjHeader) SetUid(uid Uid) {
	h.id = uid
	h.Id = uid.String()
}

// TimeNow returns current wall time in UTC rounded to milliseconds.
func TimeNow() time.Time {
	return time.Now().UTC().Round(time.Millisecond)
}

// InitTimes initializes time.Time variables in the header to current time.
func (h *ObjHeader) InitTimes() {
	if h.CreatedAt.IsZero() {
		h.CreatedAt = TimeNow()
	}
	h.UpdatedAt = h.CreatedAt
}

// MergeTimes intelligently copies time.Time variables from h2 to h.
func (h *ObjHeader) MergeTimes(h2 *ObjHeader) {
	// Set the creation time to the earliest value
	if h.CreatedAt.IsZero() || (!h2.CreatedAt.IsZero() && h2.CreatedAt.Before(h.CreatedAt)) {
		h.CreatedAt = h2.CreatedAt
	}
	// Set the update time to the latest value
	if h.UpdatedAt.Before(h2.UpdatedAt) {
		h.UpdatedAt = h2.UpdatedAt
	}
}

// StringSlice is defined so Scanner and Valuer can be attached to it.
type StringSlice []string

// Scan implements sql.Scanner interface.
func (ss *StringSlice) Scan(val interface{}) error {
	if val == nil {
		return nil
	}
	return json.Unmarshal(val.([]byte), ss)
}

// Value implements sql/driver.Valuer interface.
func (ss StringSlice) Value() (driver.Value, error) {
	return json.Marshal(ss)
}

// ObjState represents information on objects state,
// such as an indication that User or Topic is suspended/soft-deleted.
type ObjState int

const (
	// StateOK indicates normal user or topic.
	StateOK ObjState = 0
	// StateSuspended indicates suspended user or topic.
	StateSuspended ObjState = 10
	// StateDeleted indicates soft-deleted user or topic.
	StateDeleted ObjState = 20
	// StateUndefined indicates state which has not been set explicitly.
	StateUndefined ObjState = 30
)

// String returns string representation of ObjState.
func (os ObjState) String() string {
	switch os {
	case StateOK:
		return "ok"
	case StateSuspended:
		return "susp"
	case StateDeleted:
		return "del"
	case StateUndefined:
		return "undef"
	}
	return ""
}

// NewObjState parses string into an ObjState.
func NewObjState(in string) (ObjState, error) {
	in = strings.ToLower(in)
	switch in {
	case "", "ok":
		return StateOK, nil
	case "susp":
		return StateSuspended, nil
	case "del":
		return StateDeleted, nil
	case "undef":
		return StateUndefined, nil
	}
	// This is the default.
	return StateOK, errors.New("failed to parse object state")
}

// MarshalJSON converts ObjState to a quoted string.
func (os ObjState) MarshalJSON() ([]byte, error) {
	return append(append([]byte{'"'}, []byte(os.String())...), '"'), nil
}

// UnmarshalJSON reads ObjState from a quoted string.
func (os *ObjState) UnmarshalJSON(b []byte) error {
	if b[0] != '"' || b[len(b)-1] != '"' {
		return errors.New("syntax error")
	}
	state, err := NewObjState(string(b[1 : len(b)-1]))
	if err == nil {
		*os = state
	}
	return err
}

// Scan is an implementation of sql.Scanner interface. It expects the
// value to be a byte slice representation of an ASCII string.
func (os *ObjState) Scan(val interface{}) error {
	switch intval := val.(type) {
	case int64:
		*os = ObjState(intval)
		return nil
	}
	return errors.New("data is not an int64")
}

// Value is an implementation of sql.driver.Valuer interface.
func (os ObjState) Value() (driver.Value, error) {
	return int64(os), nil
}

// User is a representation of a DB-stored user record.
type User struct {
	ObjHeader `bson:",inline"`

	State   ObjState
	StateAt *time.Time `json:"StateAt,omitempty" bson:",omitempty"`

	// Default access to user for P2P topics (used as default modeGiven)
	Access DefaultAccess

	// Values for 'me' topic:

	// Last time when the user joined 'me' topic, by User Agent
	LastSeen *time.Time
	// User agent provided when accessing the topic last time
	UserAgent string

	Public interface{}

	// Unique indexed tags (email, phone) for finding this user. Stored on the
	// 'users' as well as indexed in 'tagunique'
	Tags StringSlice

	// Info on known devices, used for push notifications
	Devices map[string]*DeviceDef `bson:"__devices,skip,omitempty"`
	// Same for mongodb scheme. Ignore in other db backends if its not suitable.
	DeviceArray []*DeviceDef `json:"-" bson:"devices"`
}

// AccessMode is a definition of access mode bits.
type AccessMode uint

// Various access mode constants
const (
	ModeJoin    AccessMode = 1 << iota // user can join, i.e. {sub} (J:1)
	ModeRead                           // user can receive broadcasts ({data}, {info}) (R:2)
	ModeWrite                          // user can Write, i.e. {pub} (W:4)
	ModePres                           // user can receive presence updates (P:8)
	ModeApprove                        // user can approve new members or evict existing members (A:0x10, 16)
	ModeShare                          // user can invite new members (S:0x20, 32)
	ModeDelete                         // user can hard-delete messages (D:0x40, 64)
	ModeOwner                          // user is the owner (O:0x80, 128) - full access
	ModeUnset                          // Non-zero value to indicate unknown or undefined mode (:0x100, 256),
	// to make it different from ModeNone

	ModeNone AccessMode = 0 // No access, requests to gain access are processed normally (N:0)

	// Normal user's access to a topic ("JRWPS", 47, 0x2F)
	ModeCPublic AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeShare
	// User's subscription to 'me' and 'fnd' ("JPS", 41, 0x29)
	ModeCSelf AccessMode = ModeJoin | ModePres | ModeShare
	// Owner's subscription to a generic topic ("JRWPASDO", 255, 0xFF)
	ModeCFull AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeApprove | ModeShare | ModeDelete | ModeOwner
	// Default P2P access mode ("JRWPA", 31, 0x1F)
	ModeCP2P AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeApprove
	// Default Auth access mode for a user ("JRWPAS", 63, 0x3F).
	ModeCAuth AccessMode = ModeCP2P | ModeCPublic
	// Read-only access to topic ("JR", 3)
	ModeCReadOnly = ModeJoin | ModeRead
	// Access to 'sys' topic by a root user ("JRWPD", 79, 0x4F)
	ModeCSys = ModeJoin | ModeRead | ModeWrite | ModePres | ModeDelete

	// Admin: user who can modify access mode ("OA", dec: 144, hex: 0x90)
	ModeCAdmin = ModeOwner | ModeApprove
	// Sharer: flags which define user who can be notified of access mode changes ("OAS", dec: 176, hex: 0xB0)
	ModeCSharer = ModeCAdmin | ModeShare

	// Invalid mode to indicate an error
	ModeInvalid AccessMode = 0x100000

	// All possible valid bits (excluding ModeInvalid and ModeUnset) = 0xFF, 255
	ModeBitmask AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeApprove | ModeShare | ModeDelete | ModeOwner
)

// MarshalText converts AccessMode to ASCII byte slice.
func (m AccessMode) MarshalText() ([]byte, error) {
	if m == ModeNone {
		return []byte{'N'}, nil
	}

	if m == ModeInvalid {
		return nil, errors.New("AccessMode invalid")
	}

	var res = []byte{}
	var modes = []byte{'J', 'R', 'W', 'P', 'A', 'S', 'D', 'O'}
	for i, chr := range modes {
		if (m & (1 << uint(i))) != 0 {
			res = append(res, chr)
		}
	}
	return res, nil
}

// ParseAcs parses AccessMode from a byte array.
func ParseAcs(b []byte) (AccessMode, error) {
	m0 := ModeUnset

Loop:
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case 'J', 'j':
			m0 |= ModeJoin
		case 'R', 'r':
			m0 |= ModeRead
		case 'W', 'w':
			m0 |= ModeWrite
		case 'A', 'a':
			m0 |= ModeApprove
		case 'S', 's':
			m0 |= ModeShare
		case 'D', 'd':
			m0 |= ModeDelete
		case 'P', 'p':
			m0 |= ModePres
		case 'O', 'o':
			m0 |= ModeOwner
		case 'N', 'n':
			m0 = ModeNone // N means explicitly no access, all bits cleared
			break Loop
		default:
			return ModeUnset, errors.New("AccessMode: invalid character '" + string(b[i]) + "'")
		}
	}

	return m0, nil
}

// UnmarshalText parses access mode string as byte slice.
// Does not change the mode if the string is empty or invalid.
func (m *AccessMode) UnmarshalText(b []byte) error {
	m0, err := ParseAcs(b)
	if err != nil {
		return err
	}

	if m0 != ModeUnset {
		*m = (m0 & ModeBitmask)
	}
	return nil
}

// String returns string representation of AccessMode.
func (m AccessMode) String() string {
	res, err := m.MarshalText()
	if err != nil {
		return ""
	}
	return string(res)
}

// MarshalJSON converts AccessMode to a quoted string.
func (m AccessMode) MarshalJSON() ([]byte, error) {
	res, err := m.MarshalText()
	if err != nil {
		return nil, err
	}

	return append(append([]byte{'"'}, res...), '"'), nil
}

// UnmarshalJSON reads AccessMode from a quoted string.
func (m *AccessMode) UnmarshalJSON(b []byte) error {
	if b[0] != '"' || b[len(b)-1] != '"' {
		return errors.New("syntax error")
	}

	return m.UnmarshalText(b[1 : len(b)-1])
}

// Scan is an implementation of sql.Scanner interface. It expects the
// value to be a byte slice representation of an ASCII string.
func (m *AccessMode) Scan(val interface{}) error {
	if bb, ok := val.([]byte); ok {
		return m.UnmarshalText(bb)
	}
	return errors.New("scan failed: data is not a byte slice")
}

// Value is an implementation of sql.driver.Valuer interface.
func (m AccessMode) Value() (driver.Value, error) {
	res, err := m.MarshalText()
	if err != nil {
		return "", err
	}
	return string(res), nil
}

// BetterThan checks if grant mode allows more permissions than requested in want mode.
func (grant AccessMode) BetterThan(want AccessMode) bool {
	return ModeBitmask&grant&^want != 0
}

// BetterEqual checks if grant mode allows all permissions requested in want mode.
func (grant AccessMode) BetterEqual(want AccessMode) bool {
	return ModeBitmask&grant&want == want
}

// Delta between two modes as a string old.Delta(new). JRPAS -> JRWS: "+W-PA"
// Zero delta is an empty string ""
func (o AccessMode) Delta(n AccessMode) string {
	// Removed bits, bits present in 'old' but missing in 'new' -> '-'
	o2n := ModeBitmask & o &^ n
	var removed string
	if o2n > 0 {
		removed = o2n.String()
		if removed != "" {
			removed = "-" + removed
		}
	}

	// Added bits, bits present in 'n' but missing in 'o' -> '+'
	n2o := ModeBitmask & n &^ o
	var added string
	if n2o > 0 {
		added = n2o.String()
		if added != "" {
			added = "+" + added
		}
	}
	return added + removed
}

// ApplyMutation sets of modifies access mode:
// * if `mutation` contains either '+' or '-', attempts to apply a delta change on `m`.
// * otherwise, treats it as an assignment.
func (m *AccessMode) ApplyMutation(mutation string) error {
	if mutation == "" {
		return nil
	}
	if strings.ContainsAny(mutation, "+-") {
		return m.ApplyDelta(mutation)
	}
	return m.UnmarshalText([]byte(mutation))
}

// ApplyDelta applies the acs delta to AccessMode.
// Delta is in the same format as generated by AccessMode.Delta.
// E.g. JPRA.ApplyDelta(-PR+W) -> JWA.
func (m *AccessMode) ApplyDelta(delta string) error {
	if delta == "" || delta == "N" {
		// No updates.
		return nil
	}
	m0 := *m
	for next := 0; next+1 < len(delta) && next >= 0; {
		ch := delta[next]
		end := strings.IndexAny(delta[next+1:], "+-")
		var chunk string
		if end >= 0 {
			end += next + 1
			chunk = delta[next+1 : end]
		} else {
			chunk = delta[next+1:]
		}
		next = end
		upd, err := ParseAcs([]byte(chunk))
		if err != nil {
			return err
		}
		switch ch {
		case '+':
			if upd != ModeUnset {
				m0 |= upd & ModeBitmask
			}
		case '-':
			if upd != ModeUnset {
				m0 &^= upd & ModeBitmask
			}
		default:
			return errors.New("Invalid acs delta string: '" + delta + "'")
		}
	}
	*m = m0
	return nil
}

// IsJoiner checks if joiner flag J is set.
func (m AccessMode) IsJoiner() bool {
	return m&ModeJoin != 0
}

// IsOwner checks if owner bit O is set.
func (m AccessMode) IsOwner() bool {
	return m&ModeOwner != 0
}

// IsApprover checks if approver A bit is set.
func (m AccessMode) IsApprover() bool {
	return m&ModeApprove != 0
}

// IsAdmin check if owner O or approver A flag is set.
func (m AccessMode) IsAdmin() bool {
	return m.IsOwner() || m.IsApprover()
}

// IsSharer checks if approver A or sharer S or owner O flag is set.
func (m AccessMode) IsSharer() bool {
	return m.IsAdmin() || (m&ModeShare != 0)
}

// IsWriter checks if allowed to publish (writer flag W is set).
func (m AccessMode) IsWriter() bool {
	return m&ModeWrite != 0
}

// IsReader checks if reader flag R is set.
func (m AccessMode) IsReader() bool {
	return m&ModeRead != 0
}

// IsPresencer checks if user receives presence updates (P flag set).
func (m AccessMode) IsPresencer() bool {
	return m&ModePres != 0
}

// IsDeleter checks if user can hard-delete messages (D flag is set).
func (m AccessMode) IsDeleter() bool {
	return m&ModeDelete != 0
}

// IsZero checks if no flags are set.
func (m AccessMode) IsZero() bool {
	return m == ModeNone
}

// IsInvalid checks if mode is invalid.
func (m AccessMode) IsInvalid() bool {
	return m == ModeInvalid
}

// IsDefined checks if the mode is defined: not invalid and not unset.
// ModeNone is considered to be defined.
func (m AccessMode) IsDefined() bool {
	return m != ModeInvalid && m != ModeUnset
}

// DefaultAccess is a per-topic default access modes
type DefaultAccess struct {
	Auth AccessMode
	Anon AccessMode
}

// Scan is an implementation of Scanner interface so the value can be read from SQL DBs
// It assumes the value is serialized and stored as JSON
func (da *DefaultAccess) Scan(val interface{}) error {
	return json.Unmarshal(val.([]byte), da)
}

// Value implements sql's driver.Valuer interface.
func (da DefaultAccess) Value() (driver.Value, error) {
	return json.Marshal(da)
}

// Credential hold data needed to validate and check validity of a credential like email or phone.
type Credential struct {
	ObjHeader `bson:",inline"`
	// Credential owner
	User string
	// Verification method (email, tel, captcha, etc)
	Method string
	// Credential value - `jdoe@example.com` or `+12345678901`
	Value string
	// Expected response
	Resp string
	// If credential was successfully confirmed
	Done bool
	// Retry count
	Retries int
}

// Subscription to a topic
type Subscription struct {
	ObjHeader `bson:",inline"`
	// User who has relationship with the topic
	User string
	// Topic subscribed to
	Topic     string
	DeletedAt *time.Time `bson:",omitempty"`

	// Values persisted through subscription soft-deletion

	// ID of the latest Soft-delete operation
	DelId int
	// Last SeqId reported by user as received by at least one of his sessions
	RecvSeqId int
	// Last SeqID reported read by the user
	ReadSeqId int

	// Access mode requested by this user
	ModeWant AccessMode
	// Access mode granted to this user
	ModeGiven AccessMode
	// User's private data associated with the subscription to topic
	Private interface{}

	// Deserialized ephemeral values

	// Deserialized public value from topic or user (depends on context)
	// In case of P2P topics this is the Public value of the other user.
	public interface{}
	// deserialized SeqID from user or topic
	seqId int
	// Deserialized TouchedAt from topic
	touchedAt time.Time
	// timestamp when the user was last online
	lastSeen time.Time
	// user agent string of the last online access
	userAgent string

	// P2P only. ID of the other user
	with string
	// P2P only. Default access: this is the mode given by the other user to this user
	modeDefault *DefaultAccess

	// Topic's or user's state.
	state ObjState
}

// SetPublic assigns to public, otherwise not accessible from outside the package.
func (s *Subscription) SetPublic(pub interface{}) {
	s.public = pub
}

// GetPublic reads value of public.
func (s *Subscription) GetPublic() interface{} {
	return s.public
}

// SetWith sets other user for P2P subscriptions.
func (s *Subscription) SetWith(with string) {
	s.with = with
}

// GetWith returns the other user for P2P subscriptions.
func (s *Subscription) GetWith() string {
	return s.with
}

// GetTouchedAt returns touchedAt.
func (s *Subscription) GetTouchedAt() time.Time {
	return s.touchedAt
}

// SetTouchedAt sets the value of touchedAt.
func (s *Subscription) SetTouchedAt(touchedAt time.Time) {
	if touchedAt.After(s.touchedAt) {
		s.touchedAt = touchedAt
	}

	if s.touchedAt.Before(s.UpdatedAt) {
		s.touchedAt = s.UpdatedAt
	}
}

// GetSeqId returns seqId.
func (s *Subscription) GetSeqId() int {
	return s.seqId
}

// SetSeqId sets seqId field.
func (s *Subscription) SetSeqId(id int) {
	s.seqId = id
}

// GetLastSeen returns lastSeen.
func (s *Subscription) GetLastSeen() time.Time {
	return s.lastSeen
}

// GetUserAgent returns userAgent.
func (s *Subscription) GetUserAgent() string {
	return s.userAgent
}

// SetLastSeenAndUA updates lastSeen time and userAgent.
func (s *Subscription) SetLastSeenAndUA(when *time.Time, ua string) {
	if when != nil {
		s.lastSeen = *when
	}
	s.userAgent = ua
}

// SetDefaultAccess updates default access values.
func (s *Subscription) SetDefaultAccess(auth, anon AccessMode) {
	s.modeDefault = &DefaultAccess{auth, anon}
}

// GetDefaultAccess returns default access.
func (s *Subscription) GetDefaultAccess() *DefaultAccess {
	return s.modeDefault
}

// GetState returns topic's or user's state.
func (s *Subscription) GetState() ObjState {
	return s.state
}

// SetState assigns topic's or user's state.
func (s *Subscription) SetState(state ObjState) {
	s.state = state
}

// Contact is a result of a search for connections
type Contact struct {
	Id       string
	MatchOn  []string
	Access   DefaultAccess
	LastSeen time.Time
	Public   interface{}
}

type perUserData struct {
	private interface{}
	want    AccessMode
	given   AccessMode
}

// Topic stored in database. Topic's name is Id
type Topic struct {
	ObjHeader `bson:",inline"`

	// State of the topic: normal (ok), suspended, deleted
	State   ObjState
	StateAt *time.Time `json:"StateAt,omitempty" bson:",omitempty"`

	// Timestamp when the last message has passed through the topic
	TouchedAt time.Time

	// Use bearer token or use ACL
	UseBt bool

	// Topic owner. Could be zero
	Owner string

	// Default access to topic
	Access DefaultAccess

	// Server-issued sequential ID
	SeqId int
	// If messages were deleted, sequential id of the last operation to delete them
	DelId int

	Public interface{}

	// Indexed tags for finding this topic.
	Tags StringSlice

	// Deserialized ephemeral params
	perUser map[Uid]*perUserData // deserialized from Subscription
}

// GiveAccess updates access mode for the given user.
func (t *Topic) GiveAccess(uid Uid, want, given AccessMode) {
	if t.perUser == nil {
		t.perUser = make(map[Uid]*perUserData, 1)
	}

	pud := t.perUser[uid]
	if pud == nil {
		pud = &perUserData{}
	}

	pud.want = want
	pud.given = given

	t.perUser[uid] = pud
	if want&given&ModeOwner != 0 && t.Owner == "" {
		t.Owner = uid.String()
	}
}

// SetPrivate updates private value for the given user.
func (t *Topic) SetPrivate(uid Uid, private interface{}) {
	if t.perUser == nil {
		t.perUser = make(map[Uid]*perUserData, 1)
	}
	pud := t.perUser[uid]
	if pud == nil {
		pud = &perUserData{}
	}
	pud.private = private
	t.perUser[uid] = pud
}

// GetPrivate returns given user's private value.
func (t *Topic) GetPrivate(uid Uid) (private interface{}) {
	if t.perUser == nil {
		return
	}
	pud := t.perUser[uid]
	if pud == nil {
		return
	}
	private = pud.private
	return
}

// GetAccess returns given user's access mode.
func (t *Topic) GetAccess(uid Uid) (mode AccessMode) {
	if t.perUser == nil {
		return
	}
	pud := t.perUser[uid]
	if pud == nil {
		return
	}
	mode = pud.given & pud.want
	return
}

// SoftDelete is a single DB record of soft-deletetion.
type SoftDelete struct {
	User  string
	DelId int
}

// MessageHeaders is needed to attach Scan() to.
type MessageHeaders map[string]interface{}

// Scan implements sql.Scanner interface.
func (mh *MessageHeaders) Scan(val interface{}) error {
	return json.Unmarshal(val.([]byte), mh)
}

// Value implements sql's driver.Valuer interface.
func (mh MessageHeaders) Value() (driver.Value, error) {
	return json.Marshal(mh)
}

// Message is a stored {data} message
type Message struct {
	ObjHeader `bson:",inline"`
	DeletedAt *time.Time `json:"DeletedAt,omitempty" bson:",omitempty"`

	// ID of the hard-delete operation
	DelId int `json:"DelId,omitempty" bson:",omitempty"`
	// List of users who have marked this message as soft-deleted
	DeletedFor []SoftDelete `json:"DeletedFor,omitempty" bson:",omitempty"`
	SeqId      int
	Topic      string
	// Sender's user ID as string (without 'usr' prefix), could be empty.
	From    string
	Head    MessageHeaders `json:"Head,omitempty" bson:",omitempty"`
	Content interface{}
}

// Range is a range of message SeqIDs. Low end is inclusive (closed), high end is exclusive (open): [Low, Hi).
// If the range contains just one ID, Hi is set to 0
type Range struct {
	Low int
	Hi  int `json:"Hi,omitempty" bson:",omitempty"`
}

// RangeSorter is a helper type required by 'sort' package.
type RangeSorter []Range

// Len is the length of the range.
func (rs RangeSorter) Len() int {
	return len(rs)
}

// Swap swaps two items in a slice.
func (rs RangeSorter) Swap(i, j int) {
	rs[i], rs[j] = rs[j], rs[i]
}

// Less is a comparator. Sort by Low ascending, then sort by Hi descending
func (rs RangeSorter) Less(i, j int) bool {
	if rs[i].Low < rs[j].Low {
		return true
	}
	if rs[i].Low == rs[j].Low {
		return rs[i].Hi >= rs[j].Hi
	}
	return false
}

// Normalize ranges - remove overlaps: [1..4],[2..4],[5..7] -> [1..7].
// The ranges are expected to be sorted.
// Ranges are inclusive-inclusive, i.e. [1..3] -> 1, 2, 3.
func (rs RangeSorter) Normalize() RangeSorter {
	ll := rs.Len()
	if ll > 1 {
		prev := 0
		for i := 1; i < ll; i++ {
			if rs[prev].Low == rs[i].Low {
				// Earlier range is guaranteed to be wider or equal to the later range,
				// collapse two ranges into one (by doing nothing)
				continue
			}
			// Check for full or partial overlap
			if rs[prev].Hi > 0 && rs[prev].Hi+1 >= rs[i].Low {
				// Partial overlap
				if rs[prev].Hi < rs[i].Hi {
					rs[prev].Hi = rs[i].Hi
				}
				// Otherwise the next range is fully within the previous range, consume it by doing nothing.
				continue
			}
			// No overlap
			prev++
		}
		rs = rs[:prev+1]
	}

	return rs
}

// DelMessage is a log entry of a deleted message range.
type DelMessage struct {
	ObjHeader   `bson:",inline"`
	Topic       string
	DeletedFor  string
	DelId       int
	SeqIdRanges []Range
}

// QueryOpt is options of a query, [since, before] - both ends inclusive (closed)
type QueryOpt struct {
	// Subscription query
	User            Uid
	Topic           string
	IfModifiedSince *time.Time
	// ID-based query parameters: Messages
	Since  int
	Before int
	// Common parameter
	Limit int
}

// TopicCat is an enum of topic categories.
type TopicCat int

const (
	// TopicCatMe is a value denoting 'me' topic.
	TopicCatMe TopicCat = iota
	// TopicCatFnd is a value denoting 'fnd' topic.
	TopicCatFnd
	// TopicCatP2P is a a value denoting 'p2p topic.
	TopicCatP2P
	// TopicCatGrp is a a value denoting group topic.
	TopicCatGrp
	// TopicCatSys is a constant indicating a system topic.
	TopicCatSys
)

// GetTopicCat given topic name returns topic category.
func GetTopicCat(name string) TopicCat {
	switch name[:3] {
	case "usr":
		return TopicCatMe
	case "p2p":
		return TopicCatP2P
	case "grp":
		return TopicCatGrp
	case "fnd":
		return TopicCatFnd
	case "sys":
		return TopicCatSys
	default:
		panic("invalid topic type for name '" + name + "'")
	}
}

// DeviceDef is the data provided by connected device. Used primarily for
// push notifications.
type DeviceDef struct {
	// Device registration ID
	DeviceId string
	// Device platform (iOS, Android, Web)
	Platform string
	// Last logged in
	LastSeen time.Time
	// Device language, ISO code
	Lang string
}

// Media handling constants
const (
	// UploadStarted indicates that the upload has started but not finished yet.
	UploadStarted = iota
	// UploadCompleted indicates that the upload has completed successfully.
	UploadCompleted
	// UploadFailed indicates that the upload has failed.
	UploadFailed
)

// FileDef is a stored record of a file upload
type FileDef struct {
	ObjHeader `bson:",inline"`
	// Status of upload
	Status int
	// User who created the file
	User string
	// Type of the file.
	MimeType string
	// Size of the file in bytes.
	Size int64
	// Internal file location, i.e. path on disk or an S3 blob address.
	Location string
}

// Turns 2d slice into a 1d slice.
func FlattenDoubleSlice(data [][]string) []string {
	var result []string
	for _, el := range data {
		result = append(result, el...)
	}
	return result
}
