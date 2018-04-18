package store

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/adapter"
	"github.com/tinode/chat/server/store/types"
	"github.com/tinode/chat/server/validate"
)

var adp adapter.Adapter

// Unique ID generator
var uGen types.UidGenerator

type configType struct {
	// Name of the adapter to use.
	UseAdapter string `json:"use_adapter"`
	// 16-byte key for XTEA. Used to initialize types.UidGenerator
	UidKey []byte `json:"uid_key"`
	// Configurations for individual adapters.
	Adapters map[string]json.RawMessage `json:"adapters"`
}

func openAdapter(workerId int, useAdapter, jsonconf string) error {
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("store: failed to parse config: " + err.Error() + "(" + jsonconf + ")")
	}

	if useAdapter == "" {
		useAdapter = config.UseAdapter
	}
	adp = dbAdapters[useAdapter]
	if adp == nil {
		return errors.New("store: attept to Open an unknown adapter '" + useAdapter + "'")
	}

	if adp.IsOpen() {
		return errors.New("store: connection is already opened")
	}

	// Initialise snowflake
	if workerId < 0 || workerId > 1023 {
		return errors.New("store: invalid worker ID")
	}

	if err := uGen.Init(uint(workerId), config.UidKey); err != nil {
		return errors.New("store: failed to init snowflake: " + err.Error())
	}

	var adapter_config string
	if config.Adapters != nil {
		adapter_config = string(config.Adapters[useAdapter])
	}

	return adp.Open(adapter_config)
}

// Open initializes the persistence system. Adapter holds a connection pool for a database instance.
// 	 name - name of the adapter rquested in the config file
//   jsonconf - configuration string
func Open(workerId int, useAdapter, jsonconf string) error {
	if err := openAdapter(workerId, useAdapter, jsonconf); err != nil {
		return err
	}
	if err := adp.CheckDbVersion(); err != nil {
		return err
	}
	return nil
}

// Close terminates connection to persistent storage.
func Close() error {
	if adp.IsOpen() {
		return adp.Close()
	}

	return errors.New("store: connection already closed")
}

// IsOpen checks if persistent storage connection has been initialized.
func IsOpen() bool {
	if adp != nil {
		return adp.IsOpen()
	}

	return false
}

// InitDb creates a new database instance. If 'reset' is true it will first attempt to drop
// existing database. If jsconf is nil it will assume that the connection is already open.
// If it's non-nil, it will use the config string to open the DB connection first.
func InitDb(useAdapter, jsonconf string, reset bool) error {
	if !IsOpen() {
		if err := openAdapter(1, useAdapter, jsonconf); err != nil {
			return err
		}
	}
	return adp.CreateDb(reset)
}

// Registered database adapters.
var dbAdapters map[string]adapter.Adapter

// Register makes a persistence adapter available by the provided name.
// If Register is called twice or if the adapter is nil, it panics.
// Name is currently unused, i.e. only a single adapter can be registered
func RegisterAdapter(name string, a adapter.Adapter) {
	if dbAdapters == nil {
		dbAdapters = make(map[string]adapter.Adapter)
	}
	if a == nil {
		panic("store: Register adapter is nil")
	}

	if _, dup := dbAdapters[name]; dup {
		panic("store: duplicate registration of adapter " + name)
	}

	dbAdapters[name] = a
}

// GetUid generates a unique ID suitable for use as a primary key.
func GetUid() types.Uid {
	return uGen.Get()
}

// GetUidString generate unique ID as string
func GetUidString() string {
	return uGen.GetStr()
}

func DecodeUid(uid types.Uid) int64 {
	if uid.IsZero() {
		return 0
	}
	return uGen.DecodeUid(uid)
}

func EncodeUid(id int64) types.Uid {
	if id == 0 {
		return types.ZeroUid
	}
	return uGen.EncodeInt64(id)
}

// UsersObjMapper is a users struct to hold methods for persistence mapping for the User object.
type UsersObjMapper struct{}

// Users is the ancor for storing/retrieving User objects
var Users UsersObjMapper

// Create inserts User object into a database, updates creation time and assigns UID
func (UsersObjMapper) Create(user *types.User, private interface{}) (*types.User, error) {

	user.SetUid(GetUid())
	user.InitTimes()

	err := adp.UserCreate(user)
	if err != nil {
		return nil, err
	}

	// Create user's subscription to 'me' && 'find'. These topics are ephemeral, the topic object need not to be
	// inserted.
	err = Subs.Create(
		&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
			User:      user.Id,
			Topic:     user.Uid().UserId(),
			ModeWant:  types.ModeCSelf,
			ModeGiven: types.ModeCSelf,
			Private:   private,
		},
		&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
			User:      user.Id,
			Topic:     user.Uid().FndName(),
			ModeWant:  types.ModeCSelf,
			ModeGiven: types.ModeCSelf,
			Private:   nil,
		})
	if err != nil {
		// Best effort to delete incomplete user record. Orphaned user records are not a problem.
		// They just take up space.
		adp.UserDelete(user.Uid(), false)
		return nil, err
	}

	return user, nil
}

// GetAuthRecord takes a unique identifier and a authentication scheme name, fetches user ID and
// authentication secret.
func (UsersObjMapper) GetAuthRecord(scheme, unique string) (types.Uid, auth.Level, []byte, time.Time, error) {
	return adp.AuthGetRecord(scheme + ":" + unique)
}

// AddAuthRecord creates a new authentication record for the given user.
func (UsersObjMapper) AddAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string, secret []byte,
	expires time.Time) (bool, error) {

	return adp.AuthAddRecord(uid, authLvl, scheme+":"+unique, secret, expires)
}

// UpdateAuthRecord updates authentication record with a new secret and expiration time.
func (UsersObjMapper) UpdateAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string,
	secret []byte, expires time.Time) (int, error) {

	return adp.AuthUpdRecord(scheme+":"+unique, authLvl, secret, expires)
}

// DelAuthRecord deletes user's all auth records of the givel scheme.
func (UsersObjMapper) DelAuthRecords(uid types.Uid, scheme string) error {
	return adp.AuthDelRecord(uid, scheme)
}

// Get returns a user object for the given user id
func (UsersObjMapper) Get(uid types.Uid) (*types.User, error) {
	return adp.UserGet(uid)
}

// GetAll returns a slice of user objects for the given user ids
func (UsersObjMapper) GetAll(uid ...types.Uid) ([]types.User, error) {
	return adp.UserGetAll(uid...)
}

// Delete deletes user records.
func (UsersObjMapper) Delete(id types.Uid, soft bool) error {
	if !soft {
		adp.SubsDelForUser(id)
		// TODO: Maybe delete topics where the user is the owner and all subscriptions to those topics,
		// and messages
		adp.AuthDelAllRecords(id)
		adp.CredDel(id, "")
	}

	adp.UserDelete(id, soft)

	return errors.New("store: not implemented")
}

// UpdateLastSeen updates LastSeen and UserAgent.
func (UsersObjMapper) UpdateLastSeen(uid types.Uid, userAgent string, when time.Time) error {
	return adp.UserUpdateLastSeen(uid, userAgent, when)
}

// Update is a generic user data update.
func (UsersObjMapper) Update(uid types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adp.UserUpdate(uid, update)
}

// GetSubs loads a list of subscriptions for the given user
func (UsersObjMapper) GetSubs(id types.Uid) ([]types.Subscription, error) {
	return adp.SubsForUser(id, false)
}

// FindSubs loads a list of users for the given tags.
func (UsersObjMapper) FindSubs(id types.Uid, query []string) ([]types.Subscription, error) {
	usubs, err := adp.FindUsers(id, query)
	if err != nil {
		return nil, err
	}
	tsubs, err := adp.FindTopics(query)
	if err != nil {
		return nil, err
	}
	return append(usubs, tsubs...), nil
}

// GetTopics load a list of user's subscriptions with Public field copied to subscription
func (UsersObjMapper) GetTopics(id types.Uid) ([]types.Subscription, error) {
	return adp.TopicsForUser(id, false)
}

// GetTopicsAny load a list of user's subscriptions with Public field copied to subscription.
// Deleted topics are returned too.
func (UsersObjMapper) GetTopicsAny(id types.Uid) ([]types.Subscription, error) {
	return adp.TopicsForUser(id, true)
}

// SaveCred saves a credential validation request.
func (UsersObjMapper) SaveCred(cred *types.Credential) error {
	cred.InitTimes()
	return adp.CredAdd(cred)
}

// ConfirmCred markes credential as confirmed.
func (UsersObjMapper) ConfirmCred(id types.Uid, method string) error {
	return adp.CredConfirm(id, method)
}

// FailCred increments fail count.
func (UsersObjMapper) FailCred(id types.Uid, method string) error {
	return adp.CredFail(id, method)
}

// GetAllCred gets a list of confirmed credentials.
func (UsersObjMapper) GetCred(id types.Uid, method string) (*types.Credential, error) {
	var creds []*types.Credential
	var err error
	if creds, err = adp.CredGet(id, method); err == nil {
		if len(creds) > 0 {
			return creds[0], nil
		}
		return nil, nil
	}
	return nil, err

}

//
func (UsersObjMapper) GetAllCred(id types.Uid, method string) ([]*types.Credential, error) {
	return adp.CredGet(id, "")
}

// TopicsObjMapper is a struct to hold methods for persistence mapping for the topic object.
type TopicsObjMapper struct{}

// Topics is an instance of TopicsObjMapper to map methods to.
var Topics TopicsObjMapper

// Create creates a topic and owner's subscription to it.
func (TopicsObjMapper) Create(topic *types.Topic, owner types.Uid, private interface{}) error {

	topic.InitTimes()

	err := adp.TopicCreate(topic)
	if err != nil {
		return err
	}

	if !owner.IsZero() {
		err = Subs.Create(&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: topic.CreatedAt},
			User:      owner.String(),
			Topic:     topic.Id,
			ModeGiven: types.ModeCFull,
			ModeWant:  topic.GetAccess(owner),
			Private:   private})
	}

	return err
}

// CreateP2P creates a P2P topic by generating two user's subsciptions to each other.
func (TopicsObjMapper) CreateP2P(initiator, invited *types.Subscription) error {
	initiator.InitTimes()
	invited.InitTimes()

	return adp.TopicCreateP2P(initiator, invited)
}

// Get a single topic with a list of relevant users de-normalized into it
func (TopicsObjMapper) Get(topic string) (*types.Topic, error) {
	return adp.TopicGet(topic)
}

// GetUsers loads subscriptions for topic plus loads user.Public
func (TopicsObjMapper) GetUsers(topic string) ([]types.Subscription, error) {
	return adp.UsersForTopic(topic, false)
}

// GetUsersAny is the same as GetUsers, except it loads deleted subscriptions too.
func (TopicsObjMapper) GetUsersAny(topic string) ([]types.Subscription, error) {
	return adp.UsersForTopic(topic, true)
}

// GetSubs loads a list of subscriptions to the given topic, user.Public and deleted
// subscriptions are not loaded
func (TopicsObjMapper) GetSubs(topic string) ([]types.Subscription, error) {
	return adp.SubsForTopic(topic, false)
}

// Update is a generic topic update.
func (TopicsObjMapper) Update(topic string, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adp.TopicUpdate(topic, update)
}

// Delete deletes topic, messages and subscriptions.
func (TopicsObjMapper) Delete(topic string) error {
	if err := adp.SubsDelForTopic(topic); err != nil {
		return err
	}
	if err := adp.MessageDeleteList(topic, nil); err != nil {
		return err
	}

	return adp.TopicDelete(topic)
}

// SubsObjMapper is A struct to hold methods for persistence mapping for the Subscription object.
type SubsObjMapper struct{}

// Subs is an instance of SubsObjMapper to map methods to.
var Subs SubsObjMapper

// Create creates multiple subscriptions
func (SubsObjMapper) Create(subs ...*types.Subscription) error {
	for _, sub := range subs {
		sub.InitTimes()
	}

	_, err := adp.TopicShare(subs)
	return err
}

// Get given subscription
func (SubsObjMapper) Get(topic string, user types.Uid) (*types.Subscription, error) {
	return adp.SubscriptionGet(topic, user)
}

// Update values of topic's subscriptions.
func (SubsObjMapper) Update(topic string, user types.Uid, update map[string]interface{}, updateTS bool) error {
	if updateTS {
		update["UpdatedAt"] = types.TimeNow()
	}
	return adp.SubsUpdate(topic, user, update)
}

// Delete deletes a subscription
func (SubsObjMapper) Delete(topic string, user types.Uid) error {
	return adp.SubsDelete(topic, user)
}

// MessagesObjMapper is a struct to hold methods for persistence mapping for the Message object.
type MessagesObjMapper struct{}

// Messages is an instance of MessagesObjMapper to map methods to.
var Messages MessagesObjMapper

// Save message
func (MessagesObjMapper) Save(msg *types.Message) error {
	msg.InitTimes()

	// Increment topic's or user's SeqId
	if err := adp.TopicUpdateOnMessage(msg.Topic, msg); err != nil {
		return err
	}

	return adp.MessageSave(msg)
}

// DeleteList deletes multiple messages defined by a list of ranges.
func (MessagesObjMapper) DeleteList(topic string, delID int, forUser types.Uid, ranges []types.Range) error {
	var toDel *types.DelMessage
	if delID > 0 {
		toDel = &types.DelMessage{
			Topic:       topic,
			DelId:       delID,
			DeletedFor:  forUser.String(),
			SeqIdRanges: ranges}
		toDel.InitTimes()
	}

	err := adp.MessageDeleteList(topic, toDel)
	if err != nil {
		return err
	}

	if delID > 0 {
		// Record ID of the delete transaction
		err = adp.TopicUpdate(topic, map[string]interface{}{"DelId": delID})
		if err != nil {
			return err
		}

		// Soft-deleting will update one subscription, hard-deleting will ipdate all.
		// Soft- or hard- is defined by the forUSer being defined.
		return adp.SubsUpdate(topic, forUser, map[string]interface{}{"DelId": delID})
	}

	return nil
}

// GetAll returns multiple messages.
func (MessagesObjMapper) GetAll(topic string, forUser types.Uid, opt *types.BrowseOpt) ([]types.Message, error) {
	return adp.MessageGetAll(topic, forUser, opt)
}

// GetDeleted returns the ranges of deleted messages and the largest DelId reported in the list.
func (MessagesObjMapper) GetDeleted(topic string, forUser types.Uid, opt *types.BrowseOpt) ([]types.Range, int, error) {
	dmsgs, err := adp.MessageGetDeleted(topic, forUser, opt)
	if err != nil {
		return nil, 0, err
	}

	var ranges []types.Range
	var maxID int
	// Flatten out the ranges
	for _, dm := range dmsgs {
		if dm.DelId > maxID {
			maxID = dm.DelId
		}
		ranges = append(ranges, dm.SeqIdRanges...)
	}
	sort.Sort(types.RangeSorter(ranges))
	types.RangeSorter(ranges).Normalize()

	return ranges, maxID, nil
}

// Registered authentication handlers.
var authHandlers map[string]auth.AuthHandler

// RegisterAuthScheme registers an authentication scheme handler.
func RegisterAuthScheme(name string, handler auth.AuthHandler) {
	if authHandlers == nil {
		authHandlers = make(map[string]auth.AuthHandler)
	}

	if handler == nil {
		panic("RegisterAuthScheme: scheme handler is nil")
	}
	if _, dup := authHandlers[name]; dup {
		panic("RegisterAuthScheme: called twice for scheme " + name)
	}
	authHandlers[name] = handler
}

// GetAuthHandler returns an auth handler by name.
func GetAuthHandler(name string) auth.AuthHandler {
	return authHandlers[name]
}

// Registered authentication handlers.
var validators map[string]validate.Validator

func RegisterValidator(name string, v validate.Validator) {
	if validators == nil {
		validators = make(map[string]validate.Validator)
	}

	if v == nil {
		panic("RegisterValidator: validator is nil")
	}
	if _, dup := validators[name]; dup {
		panic("RegisterValidator: called twice for validator " + name)
	}
	validators[name] = v
}

func GetValidator(name string) validate.Validator {
	return validators[name]
}

// DeviceMapper is a struct to map methods used for handling device IDs, used to generate push notifications.
type DeviceMapper struct{}

// Devices is an instance of DeviceMapper to map methods to.
var Devices DeviceMapper

// Update updates a device record.
func (DeviceMapper) Update(uid types.Uid, oldDeviceID string, dev *types.DeviceDef) error {
	// If the old device Id is specified and it's different from the new ID, delete the old id
	if oldDeviceID != "" && (dev == nil || dev.DeviceId != oldDeviceID) {
		if err := adp.DeviceDelete(uid, oldDeviceID); err != nil {
			return err
		}
	}

	// Insert or update the new DeviceId if one is given.
	if dev != nil && dev.DeviceId != "" {
		return adp.DeviceUpsert(uid, dev)
	}
	return nil
}

// GetAll returns all known device IDS for a given list of user IDs.
func (DeviceMapper) GetAll(uid ...types.Uid) (map[types.Uid][]types.DeviceDef, int, error) {
	return adp.DeviceGetAll(uid...)
}

// Delete deletes device record for a given user.
func (DeviceMapper) Delete(uid types.Uid, deviceID string) error {
	return adp.DeviceDelete(uid, deviceID)
}
