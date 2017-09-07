package types

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// Uid is a database-specific record id, suitable to be used as a primary key.
type Uid uint64

var ZeroUid Uid = 0

const (
	uid_BASE64_UNPADDED = 11
	uid_BASE64_PADDED   = 12

	p2p_BASE64_UNPADDED = 22
	p2p_BASE64_PADDED   = 24
)

func (uid Uid) IsZero() bool {
	return uid == 0
}

// Compare returns 0 if uid is equal to u2, 1 if u2 is greater than uid, -1 if u2 is smaller
func (uid Uid) Compare(u2 Uid) int {
	if uid < u2 {
		return -1
	} else if uid > u2 {
		return 1
	}
	return 0
}

func (uid *Uid) MarshalBinary() ([]byte, error) {
	dst := make([]byte, 8)
	binary.LittleEndian.PutUint64(dst, uint64(*uid))
	return dst, nil
}

func (uid *Uid) UnmarshalBinary(b []byte) error {
	if len(b) < 8 {
		return errors.New("Uid.UnmarshalBinary: invalid length")
	}
	*uid = Uid(binary.LittleEndian.Uint64(b))
	return nil
}

func (uid *Uid) UnmarshalText(src []byte) error {
	if len(src) != uid_BASE64_UNPADDED {
		return errors.New("Uid.UnmarshalText: invalid length")
	}
	dec := make([]byte, base64.URLEncoding.DecodedLen(uid_BASE64_PADDED))
	for len(src) < uid_BASE64_PADDED {
		src = append(src, '=')
	}
	count, err := base64.URLEncoding.Decode(dec, src)
	if count < 8 {
		if err != nil {
			return errors.New("Uid.UnmarshalText: failed to decode " + err.Error())
		}
		return errors.New("Uid.UnmarshalText: failed to decode")
	}
	*uid = Uid(binary.LittleEndian.Uint64(dec))
	return nil
}

func (uid *Uid) MarshalText() ([]byte, error) {
	if *uid == 0 {
		return []byte{}, nil
	}
	src := make([]byte, 8)
	dst := make([]byte, base64.URLEncoding.EncodedLen(8))
	binary.LittleEndian.PutUint64(src, uint64(*uid))
	base64.URLEncoding.Encode(dst, src)
	return dst[0:uid_BASE64_UNPADDED], nil
}

func (uid *Uid) MarshalJSON() ([]byte, error) {
	dst, _ := uid.MarshalText()
	return append(append([]byte{'"'}, dst...), '"'), nil
}

func (uid *Uid) UnmarshalJSON(b []byte) error {
	size := len(b)
	if size != (uid_BASE64_UNPADDED + 2) {
		return errors.New("Uid.UnmarshalJSON: invalid length")
	} else if b[0] != '"' || b[size-1] != '"' {
		return errors.New("Uid.UnmarshalJSON: unrecognized")
	}
	return uid.UnmarshalText(b[1 : size-1])
}

func (uid Uid) String() string {
	buf, _ := uid.MarshalText()
	return string(buf)
}

// Parse UID parses string NOT prefixed with anything
func ParseUid(s string) Uid {
	var uid Uid
	uid.UnmarshalText([]byte(s))
	return uid
}

func (uid Uid) UserId() string {
	return uid.PrefixId("usr")
}

func (uid Uid) FndName() string {
	return uid.PrefixId("fnd")
}

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

// Given two UIDs generate a P2P topic name
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

		return "p2p" + base64.URLEncoding.EncodeToString(b1)[:p2p_BASE64_UNPADDED]
	}

	return ""
}

// ParseP2P extracts uids from the name of a p2p topic
func ParseP2P(p2p string) (uid1, uid2 Uid, err error) {
	if strings.HasPrefix(p2p, "p2p") {
		src := []byte(p2p)[3:]
		if len(src) != p2p_BASE64_UNPADDED {
			err = errors.New("ParseP2P: invalid length")
			return
		}
		dec := make([]byte, base64.URLEncoding.DecodedLen(p2p_BASE64_PADDED))
		for len(src) < p2p_BASE64_PADDED {
			src = append(src, '=')
		}
		var count int
		count, err = base64.URLEncoding.Decode(dec, src)
		if count < 16 {
			if err != nil {
				err = errors.New("ParseP2P: failed to decode " + err.Error())
			}
			err = errors.New("ParseP2P: invalid decoded length")
			return
		}
		uid1 = Uid(binary.LittleEndian.Uint64(dec))
		uid2 = Uid(binary.LittleEndian.Uint64(dec[8:]))
	} else {
		err = errors.New("ParseP2P: missing or invalid prefix")
	}
	return
}

// Header shared by all stored objects
type ObjHeader struct {
	Id        string // using string to get around rethinkdb's problems with unit64
	id        Uid
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

func (h *ObjHeader) Uid() Uid {
	if h.id.IsZero() && h.Id != "" {
		h.id.UnmarshalText([]byte(h.Id))
	}
	return h.id
}

func (h *ObjHeader) SetUid(uid Uid) {
	h.id = uid
	h.Id = uid.String()
}

func TimeNow() time.Time {
	return time.Now().UTC().Round(time.Millisecond)
}

// InitTimes initializes time.Time variables in the header to current time.
func (h *ObjHeader) InitTimes() {
	if h.CreatedAt.IsZero() {
		h.CreatedAt = TimeNow()
	}
	h.UpdatedAt = h.CreatedAt
	h.DeletedAt = nil
}

// InitTimes intelligently copies time.Time variables from h2 to h.
func (h *ObjHeader) MergeTimes(h2 *ObjHeader) {
	// Set the creation time to the earliest value
	if h.CreatedAt.IsZero() || (!h2.CreatedAt.IsZero() && h2.CreatedAt.Before(h.CreatedAt)) {
		h.CreatedAt = h2.CreatedAt
	}
	// Set the update time to the latest value
	if h.UpdatedAt.Before(h2.UpdatedAt) {
		h.UpdatedAt = h2.UpdatedAt
	}
	// Set deleted time to the latest value
	if h2.DeletedAt != nil && (h.DeletedAt == nil || h.DeletedAt.Before(*h2.DeletedAt)) {
		h.DeletedAt = h2.DeletedAt
	}
}

// True if the object is deleted.
func (h *ObjHeader) IsDeleted() bool {
	return h.DeletedAt != nil
}

// Stored user
type User struct {
	ObjHeader
	// Currently unused: Unconfirmed, Active, etc.
	State int

	// Default access to user for P2P topics (used as default modeGiven)
	Access DefaultAccess

	// Values for 'me' topic:
	// Server-issued sequence ID for messages in 'me'
	SeqId int
	// If messages were hard-deleted in the topic, id of the last deleted message
	ClearId int
	// Last time when the user joined 'me' topic, by User Agent
	LastSeen time.Time
	// User agent provided when accessing the topic last time
	UserAgent string

	Public interface{}

	// Unique indexed tags (email, phone) for finding this user. Stored on the
	// 'users' as well as indexed in 'tagunique'
	Tags []string

	// Info on known devices, used for push notifications
	Devices map[string]*DeviceDef
}

type AccessMode uint

// Various access mode constants
const (
	ModeJoin    AccessMode = 1 << iota // user can join, i.e. {sub} (J:1)
	ModeRead                           // user can receive broadcasts ({data}, {info}) (R:2)
	ModeWrite                          // user can Write, i.e. {pub} (W:4)
	ModePres                           // user can receive presence updates (P:8)
	ModeApprove                        // user can approve new members or evict existing members (A:0x10)
	ModeShare                          // user can invite new members (S:0x20)
	ModeDelete                         // user can hard-delete messages (D:0x40)
	ModeOwner                          // user is the owner (O:0x80) - full access
	ModeUnset                          // Non-zero value to indicate unknown or undefined mode (:0x100),
	// to make it different from ModeNone

	ModeNone AccessMode = 0 // No access, requests to gain access are processed normally (N)

	// Normal user's access to a topic
	ModeCPublic AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeShare
	// User's subscription to 'me' and 'fnd' - user can only read and delete incoming invites
	ModeCSelf AccessMode = ModeJoin | ModeRead | ModeDelete | ModePres
	// Owner's subscription to a generic topic
	ModeCFull AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeApprove | ModeShare | ModeDelete | ModeOwner
	// Default P2P access mode
	ModeCP2P AccessMode = ModeJoin | ModeRead | ModeWrite | ModePres | ModeApprove
	// Read-only access to topic (0x3)
	ModeCReadOnly = ModeJoin | ModeRead

	// Admin: user who can modify access mode (hex: 0x90, dec: 144)
	ModeCAdmin = ModeOwner | ModeApprove
	// Sharer: flags which define user who can be notified of access mode changes (dec: 176, hex: 0xB0)
	ModeCSharer = ModeCAdmin | ModeShare

	// Invalid mode to indicate an error
	ModeInvalid AccessMode = 0x100000
)

func (m AccessMode) MarshalText() ([]byte, error) {

	// TODO: Need to distinguish between "not set" and "no access"
	if m == 0 {
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

// Parse access mode string. Do not change the mode if the string is empty or invalid.
func (m *AccessMode) UnmarshalText(b []byte) error {
	m0 := ModeUnset

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
			m0 = 0 // N means explicitly no access, all bits cleared
			break
		default:
			return errors.New("AccessMode: invalid character '" + string(b[i]) + "'")
		}
	}

	if m0 != ModeUnset {
		*m = m0
	}
	return nil
}

func (m AccessMode) String() string {
	res, err := m.MarshalText()
	if err != nil {
		return ""
	}
	return string(res)
}

func (m AccessMode) MarshalJSON() ([]byte, error) {
	res, err := m.MarshalText()
	if err != nil {
		return nil, err
	}

	res = append([]byte{'"'}, res...)
	return append(res, '"'), nil
}

func (m *AccessMode) UnmarshalJSON(b []byte) error {
	if b[0] != '"' || b[len(b)-1] != '"' {
		return errors.New("syntax error")
	}

	return m.UnmarshalText(b[1 : len(b)-1])
}

// Check if grant mode allows all that was requested in want mode
func (grant AccessMode) BetterEqual(want AccessMode) bool {
	return grant&want == want
}

// Delta between two modes as a string old.Delta(new). JRPAS -> JRWS: "+W-PA"
// Zero delta is an empty string ""
func (o AccessMode) Delta(n AccessMode) string {

	// Removed bits, bits present in 'old' but missing in 'new' -> '-'
	o2n := o &^ n
	var removed string
	if o2n > 0 {
		removed = o2n.String()
		if removed != "" {
			removed = "-" + removed
		}
	}

	// Added bits, bits present in 'n' but missing in 'o' -> '+'
	n2o := n &^ o
	var added string
	if n2o > 0 {
		added = n2o.String()
		if added != "" {
			added = "+" + added
		}
	}
	return added + removed
}

// Check if Join flag is set
func (a AccessMode) IsJoiner() bool {
	return a&ModeJoin != 0
}

// Check if owner
func (a AccessMode) IsOwner() bool {
	return a&ModeOwner != 0
}

// Check if approver
func (a AccessMode) IsApprover() bool {
	return a&ModeApprove != 0
}

// Check if owner or approver
func (a AccessMode) IsAdmin() bool {
	return a.IsOwner() || a.IsApprover()
}

// Approver or sharer or owner
func (a AccessMode) IsSharer() bool {
	return a.IsAdmin() || (a&ModeShare != 0)
}

// Check if allowed to publish
func (a AccessMode) IsWriter() bool {
	return a&ModeWrite != 0
}

// Check if allowed to publish
func (a AccessMode) IsReader() bool {
	return a&ModeRead != 0
}

// Check if user recieves presence updates
func (a AccessMode) IsPresencer() bool {
	return a&ModePres != 0
}

// Check if user can hard-delete messages
func (a AccessMode) IsDeleter() bool {
	return a&ModeDelete != 0
}

// Check if not set
func (a AccessMode) IsZero() bool {
	return a == 0
}

// Check if mode is invalid
func (a AccessMode) IsInvalid() bool {
	return a == ModeInvalid
}

// Relationship between users & topics, stored in database as Subscription
type TopicAccess struct {
	User  string
	Topic string
	Want  AccessMode
	Given AccessMode
}

// Per-topic default access modes
type DefaultAccess struct {
	Auth AccessMode
	Anon AccessMode
}

// Subscription to a topic
type Subscription struct {
	ObjHeader
	// User who has relationship with the topic
	User string
	// Topic subscribed to
	Topic string

	// Subscription state, currently unused
	State int

	// Values persisted through subscription soft-deletion

	// User soft-deleted messages equal or lower to this seq ID
	ClearId int
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
	// Id of the last hard-deleted message deserialized from user or topic
	hardClearId int
	// timestamp when the user was last online
	lastSeen time.Time
	// user agent string of the last online access
	userAgent string

	// P2P only. ID of the other user
	with string
	// P2P only. Default access: this is the mode given by the other user to this user
	modeDefault *DefaultAccess
}

// SetPublic assigns to public, otherwise not accessible from outside the package
func (s *Subscription) SetPublic(pub interface{}) {
	s.public = pub
}

func (s *Subscription) GetPublic() interface{} {
	return s.public
}

func (s *Subscription) SetWith(with string) {
	s.with = with
}

func (s *Subscription) GetWith() string {
	return s.with
}

func (s *Subscription) GetSeqId() int {
	return s.seqId
}

func (s *Subscription) SetSeqId(id int) {
	s.seqId = id
}

func (s *Subscription) GetHardClearId() int {
	return s.hardClearId
}

func (s *Subscription) SetHardClearId(id int) {
	s.hardClearId = id
}

func (s *Subscription) GetLastSeen() time.Time {
	return s.lastSeen
}

func (s *Subscription) GetUserAgent() string {
	return s.userAgent
}

func (s *Subscription) SetLastSeenAndUA(when time.Time, ua string) {
	s.lastSeen = when
	s.userAgent = ua
}

func (s *Subscription) SetDefaultAccess(auth, anon AccessMode) {
	s.modeDefault = &DefaultAccess{auth, anon}
}

func (s *Subscription) GetDefaultAccess() *DefaultAccess {
	return s.modeDefault
}

// Result of a search for connections
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

// Topic stored in database
type Topic struct {
	ObjHeader
	State int

	// Name  string -- topic name is stored in Id

	// Use bearer token or use ACL
	UseBt bool

	// Default access to topic
	Access DefaultAccess

	// Server-issued sequential ID
	SeqId int
	// If messages were deleted, id of the last deleted message
	ClearId int

	Public interface{}

	// Deserialized ephemeral params
	owner   Uid                  // first assigned owner
	perUser map[Uid]*perUserData // deserialized from Subscription
}

//func (t *Topic) GetAccessList() []TopicAccess {
//	return t.users
//}

func (t *Topic) GiveAccess(uid Uid, want AccessMode, given AccessMode) {
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
	if want&given&ModeOwner != 0 && t.owner.IsZero() {
		t.owner = uid
	}
}

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

func (t *Topic) GetOwner() Uid {
	return t.owner
}

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

type SoftDelete struct {
	User      string
	Timestamp time.Time
}

// Stored {data} message
type Message struct {
	ObjHeader
	// List of users who have marked this message as soft-deleted
	DeletedFor []SoftDelete
	SeqId      int
	Topic      string
	// UID as string of the user who sent the message, could be empty
	From    string
	Head    map[string]string
	Content interface{}
}

// Announcements/Invites
/*
type AnnounceAction int

const (
	// An invitation to subscribe
	AnnInv AnnounceAction = iota
	// A topic admin is asked to aprove a subscription
	AnnAppr
	// Change notification: request approved or subscribed by a third party or some such, no action required
	AnnUpd
	// Unsubscribe succeeded or unsubscribed by a third party or topic deleted
	AnnDel
)

func (a AnnounceAction) String() string {
	switch a {
	case AnnInv:
		return "inv"
	case AnnAppr:
		return "appr"
	case AnnUpd:
		return "upd"
	case AnnDel:
		return "del"
	}
	return ""
}
*/

type BrowseOpt struct {
	Since  int
	After  *time.Time
	Before int
	Until  *time.Time
	ByTime bool
	Limit  uint
}

type TopicCat int

const (
	TopicCat_Me TopicCat = iota
	TopicCat_Fnd
	TopicCat_P2P
	TopicCat_Grp
)

func GetTopicCat(name string) TopicCat {
	switch name[:3] {
	case "usr":
		return TopicCat_Me
	case "p2p":
		return TopicCat_P2P
	case "grp":
		return TopicCat_Grp
	case "fnd":
		return TopicCat_Fnd
	default:
		panic("invalid topic type for name '" + name + "'")
	}
}

// Data provided by connected device. Used primarily for
// push notifications
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
