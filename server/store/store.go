// Package store provides methods for registering and accessing database adapters.
package store

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/tinode/chat/server/logs"

	"github.com/tinode/chat/server/auth"
	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store/types"
	"github.com/tinode/chat/server/validate"
)

var adp adapter.Adapter
var availableAdapters = make(map[string]adapter.Adapter)
var mediaHandler media.Handler

// Unique ID generator
var uGen types.UidGenerator

type configType struct {
	// 16-byte key for XTEA. Used to initialize types.UidGenerator.
	UidKey []byte `json:"uid_key"`
	// Maximum number of results to return from adapter.
	MaxResults int `json:"max_results"`
	// DB adapter name to use. Should be one of those specified in `Adapters`.
	UseAdapter string `json:"use_adapter"`
	// Configurations for individual adapters.
	Adapters map[string]json.RawMessage `json:"adapters"`
}

func openAdapter(workerId int, jsonconf json.RawMessage) error {
	var config configType
	if err := json.Unmarshal(jsonconf, &config); err != nil {
		return errors.New("store: failed to parse config: " + err.Error() + "(" + string(jsonconf) + ")")
	}

	if adp == nil {
		if len(config.UseAdapter) > 0 {
			// Adapter name specified explicitly.
			if ad, ok := availableAdapters[config.UseAdapter]; ok {
				adp = ad
			} else {
				return errors.New("store: " + config.UseAdapter + " adapter is not available in this binary")
			}
		} else if len(availableAdapters) == 1 {
			// Default to the only entry in availableAdapters.
			for _, v := range availableAdapters {
				adp = v
			}
		} else {
			return errors.New("store: db adapter is not specified. Please set `store_config.use_adapter` in `tinode.conf`")
		}
	}

	if adp.IsOpen() {
		return errors.New("store: connection is already opened")
	}

	// Initialize snowflake.
	if workerId < 0 || workerId > 1023 {
		return errors.New("store: invalid worker ID")
	}

	if err := uGen.Init(uint(workerId), config.UidKey); err != nil {
		return errors.New("store: failed to init snowflake: " + err.Error())
	}

	if err := adp.SetMaxResults(config.MaxResults); err != nil {
		return err
	}

	var adapterConfig json.RawMessage
	if config.Adapters != nil {
		adapterConfig = config.Adapters[adp.GetName()]
	}

	return adp.Open(adapterConfig)
}

// PersistentStorageInterface defines methods used for interation with persistent storage.
type PersistentStorageInterface interface {
	Open(workerId int, jsonconf json.RawMessage) error
	Close() error
	IsOpen() bool
	GetAdapter() adapter.Adapter
	GetAdapterName() string
	GetAdapterVersion() int
	GetDbVersion() int
	InitDb(jsonconf json.RawMessage, reset bool) error
	UpgradeDb(jsonconf json.RawMessage) error
	GetUid() types.Uid
	GetUidString() string
	DbStats() func() interface{}
	GetAuthNames() []string
	GetAuthHandler(name string) auth.AuthHandler
	GetLogicalAuthHandler(name string) auth.AuthHandler
	GetValidator(name string) validate.Validator
	GetMediaHandler() media.Handler
	UseMediaHandler(name, config string) error
}

// Store is the main object for interacting with persistent storage.
var Store PersistentStorageInterface

type storeObj struct{}

// Open initializes the persistence system. Adapter holds a connection pool for a database instance.
// 	 name - name of the adapter rquested in the config file
//   jsonconf - configuration string
func (storeObj) Open(workerId int, jsonconf json.RawMessage) error {
	if err := openAdapter(workerId, jsonconf); err != nil {
		return err
	}

	return adp.CheckDbVersion()
}

// Close terminates connection to persistent storage.
func (storeObj) Close() error {
	if adp.IsOpen() {
		return adp.Close()
	}

	return nil
}

// IsOpen checks if persistent storage connection has been initialized.
func (storeObj) IsOpen() bool {
	if adp != nil {
		return adp.IsOpen()
	}

	return false
}

// GetAdapter returns the currently configured adapter.
func (storeObj) GetAdapter() adapter.Adapter {
	return adp
}

// GetAdapterName returns the name of the current adater.
func (storeObj) GetAdapterName() string {
	if adp != nil {
		return adp.GetName()
	}

	return ""
}

// GetAdapterVersion returns version of the current adater.
func (storeObj) GetAdapterVersion() int {
	if adp != nil {
		return adp.Version()
	}

	return -1
}

// GetDbVersion returns version of the underlying database.
func (storeObj) GetDbVersion() int {
	if adp != nil {
		vers, _ := adp.GetDbVersion()
		return vers
	}

	return -1
}

// InitDb creates and configures a new database instance. If 'reset' is true it will first
// attempt to drop an existing database. If jsconf is nil it will assume that the adapter is
// already open. If it's non-nil and the adapter is not open, it will use the config string
// to open the adapter first.
func (s storeObj) InitDb(jsonconf json.RawMessage, reset bool) error {
	if !s.IsOpen() {
		if err := openAdapter(1, jsonconf); err != nil {
			return err
		}
	}
	return adp.CreateDb(reset)
}

// UpgradeDb performes an upgrade of the database to the current adapter version.
// If jsconf is nil it will assume that the adapter is already open. If it's non-nil and the
// adapter is not open, it will use the config string to open the adapter first.
func (s storeObj) UpgradeDb(jsonconf json.RawMessage) error {
	if !s.IsOpen() {
		if err := openAdapter(1, jsonconf); err != nil {
			return err
		}
	}
	return adp.UpgradeDb()
}

// RegisterAdapter makes a persistence adapter available.
// If Register is called twice or if the adapter is nil, it panics.
func RegisterAdapter(a adapter.Adapter) {
	if a == nil {
		panic("store: Register adapter is nil")
	}

	adapterName := a.GetName()
	if _, ok := availableAdapters[adapterName]; ok {
		panic("store: adapter '" + adapterName + "' is already registered")
	}
	availableAdapters[adapterName] = a
}

// GetUid generates a unique ID suitable for use as a primary key.
func (storeObj) GetUid() types.Uid {
	return uGen.Get()
}

// GetUidString generate unique ID as string
func (storeObj) GetUidString() string {
	return uGen.GetStr()
}

// DecodeUid takes an XTEA encrypted Uid and decrypts it into an int64.
// This is needed for sql compatibility. Tte original int64 values
// are generated by snowflake which ensures that the top bit is unset.
func DecodeUid(uid types.Uid) int64 {
	if uid.IsZero() {
		return 0
	}
	return uGen.DecodeUid(uid)
}

// EncodeUid applies XTEA encryption to an int64 value. It's the inverse of DecodeUid.
func EncodeUid(id int64) types.Uid {
	if id == 0 {
		return types.ZeroUid
	}
	return uGen.EncodeInt64(id)
}

// DbStats returns a callback returning db connection stats object.
func (s storeObj) DbStats() func() interface{} {
	if !s.IsOpen() {
		return nil
	}
	return adp.Stats
}

// UsersPersistenceInterface is an interface which defines methods for persistent storage of user records.
type UsersPersistenceInterface interface {
	Create(user *types.User, private interface{}) (*types.User, error)
	GetAuthRecord(user types.Uid, scheme string) (string, auth.Level, []byte, time.Time, error)
	GetAuthUniqueRecord(scheme, unique string) (types.Uid, auth.Level, []byte, time.Time, error)
	AddAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string, secret []byte, expires time.Time) error
	UpdateAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string, secret []byte, expires time.Time) error
	DelAuthRecords(uid types.Uid, scheme string) error
	Get(uid types.Uid) (*types.User, error)
	GetAll(uid ...types.Uid) ([]types.User, error)
	GetByCred(method, value string) (types.Uid, error)
	Delete(id types.Uid, hard bool) error
	UpdateLastSeen(uid types.Uid, userAgent string, when time.Time) error
	Update(uid types.Uid, update map[string]interface{}) error
	UpdateTags(uid types.Uid, add, remove, reset []string) ([]string, error)
	UpdateState(uid types.Uid, state types.ObjState) error
	GetSubs(id types.Uid) ([]types.Subscription, error)
	FindSubs(id types.Uid, required [][]string, optional []string) ([]types.Subscription, error)
	GetTopics(id types.Uid, opts *types.QueryOpt) ([]types.Subscription, error)
	GetTopicsAny(id types.Uid, opts *types.QueryOpt) ([]types.Subscription, error)
	GetOwnTopics(id types.Uid) ([]string, error)
	GetChannels(id types.Uid) ([]string, error)
	UpsertCred(cred *types.Credential) (bool, error)
	ConfirmCred(id types.Uid, method string) error
	FailCred(id types.Uid, method string) error
	GetActiveCred(id types.Uid, method string) (*types.Credential, error)
	GetAllCreds(id types.Uid, method string, validatedOnly bool) ([]types.Credential, error)
	DelCred(id types.Uid, method, value string) error
	GetUnreadCount(id types.Uid) (int, error)
}

// usersMapper is a concrete type which implements UsersPersistenceInterface.
type usersMapper struct{}

// Users is a singleton ancor object exporting UsersPersistenceInterface methods.
var Users UsersPersistenceInterface

// Create inserts User object into a database, updates creation time and assigns UID
func (usersMapper) Create(user *types.User, private interface{}) (*types.User, error) {

	user.SetUid(Store.GetUid())
	user.InitTimes()

	err := adp.UserCreate(user)
	if err != nil {
		return nil, err
	}

	// Create user's subscription to 'me' && 'fnd'. These topics are ephemeral, the topic object need not to be
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
		adp.UserDelete(user.Uid(), true)
		return nil, err
	}

	return user, nil
}

// GetAuthRecord takes a user ID and a authentication scheme name, fetches unique scheme-dependent identifier and
// authentication secret.
func (usersMapper) GetAuthRecord(user types.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	unique, authLvl, secret, expires, err := adp.AuthGetRecord(user, scheme)
	if err == nil {
		parts := strings.Split(unique, ":")
		if len(parts) > 1 {
			unique = parts[1]
		} else {
			err = types.ErrInternal
		}
	}

	return unique, authLvl, secret, expires, err
}

// GetAuthUniqueRecord takes a unique identifier and a authentication scheme name, fetches user ID and
// authentication secret.
func (usersMapper) GetAuthUniqueRecord(scheme, unique string) (types.Uid, auth.Level, []byte, time.Time, error) {
	return adp.AuthGetUniqueRecord(scheme + ":" + unique)
}

// AddAuthRecord creates a new authentication record for the given user.
func (usersMapper) AddAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string, secret []byte,
	expires time.Time) error {

	return adp.AuthAddRecord(uid, scheme, scheme+":"+unique, authLvl, secret, expires)
}

// UpdateAuthRecord updates authentication record with a new secret and expiration time.
func (usersMapper) UpdateAuthRecord(uid types.Uid, authLvl auth.Level, scheme, unique string,
	secret []byte, expires time.Time) error {

	return adp.AuthUpdRecord(uid, scheme, scheme+":"+unique, authLvl, secret, expires)
}

// DelAuthRecords deletes user's auth records of the given scheme.
func (usersMapper) DelAuthRecords(uid types.Uid, scheme string) error {
	return adp.AuthDelScheme(uid, scheme)
}

// Get returns a user object for the given user id
func (usersMapper) Get(uid types.Uid) (*types.User, error) {
	return adp.UserGet(uid)
}

// GetAll returns a slice of user objects for the given user ids
func (usersMapper) GetAll(uid ...types.Uid) ([]types.User, error) {
	return adp.UserGetAll(uid...)
}

// GetByCred returns user ID for the given validated credential.
func (usersMapper) GetByCred(method, value string) (types.Uid, error) {
	return adp.UserGetByCred(method, value)
}

// Delete deletes user records.
func (usersMapper) Delete(id types.Uid, hard bool) error {
	return adp.UserDelete(id, hard)
}

// UpdateLastSeen updates LastSeen and UserAgent.
func (usersMapper) UpdateLastSeen(uid types.Uid, userAgent string, when time.Time) error {
	return adp.UserUpdate(uid, map[string]interface{}{"LastSeen": when, "UserAgent": userAgent})
}

// Update is a general-purpose update of user data.
func (usersMapper) Update(uid types.Uid, update map[string]interface{}) error {
	if _, ok := update["UpdatedAt"]; !ok {
		update["UpdatedAt"] = types.TimeNow()
	}
	return adp.UserUpdate(uid, update)
}

// UpdateTags either adds, removes, or resets tags to the given slices.
func (usersMapper) UpdateTags(uid types.Uid, add, remove, reset []string) ([]string, error) {
	return adp.UserUpdateTags(uid, add, remove, reset)
}

// UpdateState changes user's state and state of some topics associated with the user.
func (usersMapper) UpdateState(uid types.Uid, state types.ObjState) error {
	update := map[string]interface{}{
		"State":   state,
		"StateAt": types.TimeNow()}
	return adp.UserUpdate(uid, update)
}

// GetSubs loads *all* subscriptions for the given user.
// Does not load Public/Trusted or Private, does not load deleted subscriptions.
func (usersMapper) GetSubs(id types.Uid) ([]types.Subscription, error) {
	return adp.SubsForUser(id)
}

// FindSubs find a list of users and topics for the given tags. Results are formatted as subscriptions.
// `required` specifies an AND of ORs for required terms:
// at least one element of every sublist in `required` must be present in the object's tags list.
// `optional` specifies a list of optional terms.
func (usersMapper) FindSubs(id types.Uid, required [][]string, optional []string) ([]types.Subscription, error) {
	usubs, err := adp.FindUsers(id, required, optional)
	if err != nil {
		return nil, err
	}
	tsubs, err := adp.FindTopics(required, optional)
	if err != nil {
		return nil, err
	}

	allSubs := append(usubs, tsubs...)
	for i := range allSubs {
		// Indicate that the returned access modes are not 'N', but rather undefined.
		allSubs[i].ModeGiven = types.ModeUnset
		allSubs[i].ModeWant = types.ModeUnset
	}

	return allSubs, nil
}

// GetTopics load a list of user's subscriptions with Public+Trusted fields copied to subscription
func (usersMapper) GetTopics(id types.Uid, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.TopicsForUser(id, false, opts)
}

// GetTopicsAny load a list of user's subscriptions with Public+Trusted fields copied to subscription.
// Deleted topics are returned too.
func (usersMapper) GetTopicsAny(id types.Uid, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.TopicsForUser(id, true, opts)
}

// GetOwnTopics returns a slice of group topic names where the user is the owner.
func (usersMapper) GetOwnTopics(id types.Uid) ([]string, error) {
	return adp.OwnTopics(id)
}

// GetChannels returns a slice of group topic names where the user is a channel reader.
func (usersMapper) GetChannels(id types.Uid) ([]string, error) {
	return adp.ChannelsForUser(id)
}

// UpsertCred adds or updates a credential validation request. Return true if the record was inserted, false if updated.
func (usersMapper) UpsertCred(cred *types.Credential) (bool, error) {
	cred.InitTimes()
	return adp.CredUpsert(cred)
}

// ConfirmCred marks credential method as confirmed.
func (usersMapper) ConfirmCred(id types.Uid, method string) error {
	return adp.CredConfirm(id, method)
}

// FailCred increments fail count for the given credential method.
func (usersMapper) FailCred(id types.Uid, method string) error {
	return adp.CredFail(id, method)
}

// GetActiveCred gets a the currently active credential for the given user and method.
func (usersMapper) GetActiveCred(id types.Uid, method string) (*types.Credential, error) {
	return adp.CredGetActive(id, method)
}

// GetAllCreds returns credentials of the given user, all or validated only.
func (usersMapper) GetAllCreds(id types.Uid, method string, validatedOnly bool) ([]types.Credential, error) {
	return adp.CredGetAll(id, method, validatedOnly)
}

// DelCred deletes user's credentials. If method is "", all credentials are deleted.
func (usersMapper) DelCred(id types.Uid, method, value string) error {
	return adp.CredDel(id, method, value)
}

// GetUnreadCount returs user's total count of unread messages in all topics with the R permissions
func (usersMapper) GetUnreadCount(id types.Uid) (int, error) {
	return adp.UserUnreadCount(id)
}

// TopicsPersistenceInterface is an interface which defines methods for persistent storage of topics.
type TopicsPersistenceInterface interface {
	Create(topic *types.Topic, owner types.Uid, private interface{}) error
	CreateP2P(initiator, invited *types.Subscription) error
	Get(topic string) (*types.Topic, error)
	GetUsers(topic string, opts *types.QueryOpt) ([]types.Subscription, error)
	GetUsersAny(topic string, opts *types.QueryOpt) ([]types.Subscription, error)
	GetSubs(topic string, opts *types.QueryOpt) ([]types.Subscription, error)
	GetSubsAny(topic string, opts *types.QueryOpt) ([]types.Subscription, error)
	Update(topic string, update map[string]interface{}) error
	OwnerChange(topic string, newOwner types.Uid) error
	Delete(topic string, hard bool) error
}

// topicsMapper is a concrete type implementing TopicsPersistenceInterface.
type topicsMapper struct{}

// Topics is a singleton ancor object exporting TopicsPersistenceInterface methods.
var Topics TopicsPersistenceInterface

// Create creates a topic and owner's subscription to it.
func (topicsMapper) Create(topic *types.Topic, owner types.Uid, private interface{}) error {

	topic.InitTimes()
	topic.TouchedAt = topic.CreatedAt
	topic.Owner = owner.String()

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
func (topicsMapper) CreateP2P(initiator, invited *types.Subscription) error {
	initiator.InitTimes()
	initiator.SetTouchedAt(initiator.CreatedAt)
	invited.InitTimes()
	invited.SetTouchedAt(invited.CreatedAt)

	return adp.TopicCreateP2P(initiator, invited)
}

// Get a single topic with a list of relevant users de-normalized into it
func (topicsMapper) Get(topic string) (*types.Topic, error) {
	return adp.TopicGet(topic)
}

// GetUsers loads subscriptions for topic plus loads user.Public+Trusted.
// Deleted subscriptions are not loaded.
func (topicsMapper) GetUsers(topic string, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.UsersForTopic(topic, false, opts)
}

// GetUsersAny loads subscriptions for topic plus loads user.Public+Trusted. It's the same as GetUsers,
// except it loads deleted subscriptions too.
func (topicsMapper) GetUsersAny(topic string, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.UsersForTopic(topic, true, opts)
}

// GetSubs loads a list of subscriptions to the given topic, user.Public+Trusted and deleted
// subscriptions are not loaded. Suspended subscriptions are loaded.
func (topicsMapper) GetSubs(topic string, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.SubsForTopic(topic, false, opts)
}

// GetSubsAny loads a list of subscriptions to the given topic including deleted subscription.
// user.Public/Trusted are not loaded
func (topicsMapper) GetSubsAny(topic string, opts *types.QueryOpt) ([]types.Subscription, error) {
	return adp.SubsForTopic(topic, true, opts)
}

// Update is a generic topic update.
func (topicsMapper) Update(topic string, update map[string]interface{}) error {
	if _, ok := update["UpdatedAt"]; !ok {
		update["UpdatedAt"] = types.TimeNow()
	}
	return adp.TopicUpdate(topic, update)
}

// OwnerChange replaces the old topic owner with the new owner.
func (topicsMapper) OwnerChange(topic string, newOwner types.Uid) error {
	return adp.TopicOwnerChange(topic, newOwner)
}

// Delete deletes topic, messages, attachments, and subscriptions.
func (topicsMapper) Delete(topic string, hard bool) error {
	return adp.TopicDelete(topic, hard)
}

// SubsPersistenceInterface is an interface which defines methods for persistent storage of subscriptions.
type SubsPersistenceInterface interface {
	Create(subs ...*types.Subscription) error
	Get(topic string, user types.Uid, keepDeleted bool) (*types.Subscription, error)
	Update(topic string, user types.Uid, update map[string]interface{}) error
	Delete(topic string, user types.Uid) error
}

// subsMapper is a concrete type implementing SubsPersistenceInterface.
type subsMapper struct{}

// Subs is a singleton ancor object exporting SubsPersistenceInterface.
var Subs SubsPersistenceInterface

// Create creates multiple subscriptions
func (subsMapper) Create(subs ...*types.Subscription) error {
	for _, sub := range subs {
		sub.InitTimes()
	}

	return adp.TopicShare(subs)
}

// Get subscription given topic and user ID.
func (subsMapper) Get(topic string, user types.Uid, keepDeleted bool) (*types.Subscription, error) {
	return adp.SubscriptionGet(topic, user, keepDeleted)
}

// Update values of topic's subscriptions.
func (subsMapper) Update(topic string, user types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adp.SubsUpdate(topic, user, update)
}

// Delete deletes a subscription
func (subsMapper) Delete(topic string, user types.Uid) error {
	return adp.SubsDelete(topic, user)
}

// MessagesPersistenceInterface is an interface which defines methods for persistent storage of messages.
type MessagesPersistenceInterface interface {
	Save(msg *types.Message, attachmentURLs []string, readBySender bool) error
	DeleteList(topic string, delID int, forUser types.Uid, ranges []types.Range) error
	GetAll(topic string, forUser types.Uid, opt *types.QueryOpt) ([]types.Message, error)
	GetDeleted(topic string, forUser types.Uid, opt *types.QueryOpt) ([]types.Range, int, error)
}

// messagesMapper is a concrete type implementing MessagesPersistenceInterface.
type messagesMapper struct{}

// Messages is a singleton ancor object for exporting MessagesPersistenceInterface.
var Messages MessagesPersistenceInterface

// Save message
func (messagesMapper) Save(msg *types.Message, attachmentURLs []string, readBySender bool) error {
	msg.InitTimes()
	msg.SetUid(Store.GetUid())
	// Increment topic's or user's SeqId
	err := adp.TopicUpdateOnMessage(msg.Topic, msg)
	if err != nil {
		return err
	}

	err = adp.MessageSave(msg)
	if err != nil {
		return err
	}

	// Mark message as read by the sender.
	if readBySender {
		// Make sure From is valid, otherwise we will reset values for all subscribers.
		fromUid := types.ParseUid(msg.From)
		if !fromUid.IsZero() {
			// Ignore the error here. It's not a big deal if it fails.
			adp.SubsUpdate(msg.Topic, fromUid,
				map[string]interface{}{
					"RecvSeqId": msg.SeqId,
					"ReadSeqId": msg.SeqId})
		}
	}

	if len(attachmentURLs) > 0 {
		var attachments []string
		for _, url := range attachmentURLs {
			// Convert attachment URLs to file IDs.
			if fid := mediaHandler.GetIdFromUrl(url); !fid.IsZero() {
				attachments = append(attachments, fid.String())
			}
		}
		if len(attachments) > 0 {
			return adp.FileLinkAttachments("", types.ZeroUid, msg.Uid(), attachments)
		}
	}

	return nil
}

// DeleteList deletes multiple messages defined by a list of ranges.
func (messagesMapper) DeleteList(topic string, delID int, forUser types.Uid, ranges []types.Range) error {
	var toDel *types.DelMessage
	if delID > 0 {
		toDel = &types.DelMessage{
			Topic:       topic,
			DelId:       delID,
			DeletedFor:  forUser.String(),
			SeqIdRanges: ranges}
		toDel.SetUid(Store.GetUid())
		toDel.InitTimes()
	}

	err := adp.MessageDeleteList(topic, toDel)
	if err != nil {
		return err
	}

	// TODO: move to adapter.
	if delID > 0 {
		// Record ID of the delete transaction
		err = adp.TopicUpdate(topic, map[string]interface{}{"DelId": delID})
		if err != nil {
			return err
		}

		// Soft-deleting will update one subscription, hard-deleting will ipdate all.
		// Soft- or hard- is defined by the forUser being defined.
		err = adp.SubsUpdate(topic, forUser, map[string]interface{}{"DelId": delID})
		if err != nil {
			return err
		}
	}

	return err
}

// GetAll returns multiple messages.
func (messagesMapper) GetAll(topic string, forUser types.Uid, opt *types.QueryOpt) ([]types.Message, error) {
	return adp.MessageGetAll(topic, forUser, opt)
}

// GetDeleted returns the ranges of deleted messages and the largest DelId reported in the list.
func (messagesMapper) GetDeleted(topic string, forUser types.Uid, opt *types.QueryOpt) ([]types.Range, int, error) {
	dmsgs, err := adp.MessageGetDeleted(topic, forUser, opt)
	if err != nil {
		return nil, 0, err
	}

	var ranges []types.Range
	var maxID int
	// Flatten out the ranges
	for i := range dmsgs {
		dm := &dmsgs[i]
		if dm.DelId > maxID {
			maxID = dm.DelId
		}
		ranges = append(ranges, dm.SeqIdRanges...)
	}
	sort.Sort(types.RangeSorter(ranges))
	ranges = types.RangeSorter(ranges).Normalize()

	return ranges, maxID, nil
}

// Registered authentication handlers.
var authHandlers map[string]auth.AuthHandler

// Logical auth handler names
var authHandlerNames map[string]string

// RegisterAuthScheme registers an authentication scheme handler.
// The 'name' must be the hardcoded name, NOT the logical name.
func RegisterAuthScheme(name string, handler auth.AuthHandler) {
	if name == "" {
		panic("RegisterAuthScheme: empty auth scheme name")
	}
	if handler == nil {
		panic("RegisterAuthScheme: scheme handler is nil")
	}

	name = strings.ToLower(name)
	if authHandlers == nil {
		authHandlers = make(map[string]auth.AuthHandler)
	}
	if _, dup := authHandlers[name]; dup {
		panic("RegisterAuthScheme: called twice for scheme " + name)
	}
	authHandlers[name] = handler
}

// GetAuthNames returns all addressable auth handler names, logical and hardcoded
// excluding those which are disabled like "basic:".
func (s storeObj) GetAuthNames() []string {
	if len(authHandlers) == 0 {
		return nil
	}

	allNames := make(map[string]struct{})
	for name := range authHandlers {
		allNames[name] = struct{}{}
	}
	for name := range authHandlerNames {
		allNames[name] = struct{}{}
	}

	var names []string
	for name := range allNames {
		if s.GetLogicalAuthHandler(name) != nil {
			names = append(names, name)
		}
	}

	return names

}

// GetAuthHandler returns an auth handler by actual hardcoded name irrspectful of logical naming.
func (storeObj) GetAuthHandler(name string) auth.AuthHandler {
	return authHandlers[strings.ToLower(name)]
}

// GetLogicalAuthHandler returns an auth handler by logical name. If there is no handler by that
// logical name it tries to find one by the hardcoded name.
func (storeObj) GetLogicalAuthHandler(name string) auth.AuthHandler {
	name = strings.ToLower(name)
	if len(authHandlerNames) != 0 {
		if lname, ok := authHandlerNames[name]; ok {
			return authHandlers[lname]
		}
	}
	return authHandlers[name]
}

// InitAuthLogicalNames initializes authentication mapping "logical handler name":"actual handler name".
// Logical name must not be empty, actual name could be an empty string.
func InitAuthLogicalNames(config json.RawMessage) error {
	if config == nil || string(config) == "null" {
		return nil
	}
	var mapping []string
	if err := json.Unmarshal(config, &mapping); err != nil {
		return errors.New("store: failed to parse logical auth names: " + err.Error() + "(" + string(config) + ")")
	}
	if len(mapping) == 0 {
		return nil
	}

	if authHandlerNames == nil {
		authHandlerNames = make(map[string]string)
	}
	for _, pair := range mapping {
		if parts := strings.Split(pair, ":"); len(parts) == 2 {
			if parts[0] == "" {
				return errors.New("store: empty logical auth name '" + pair + "'")
			}
			parts[0] = strings.ToLower(parts[0])
			if _, ok := authHandlerNames[parts[0]]; ok {
				return errors.New("store: duplicate mapping for logical auth name '" + pair + "'")
			}
			parts[1] = strings.ToLower(parts[1])
			if parts[1] != "" {
				if _, ok := authHandlers[parts[1]]; !ok {
					return errors.New("store: unknown handler for logical auth name '" + pair + "'")
				}
			}
			if parts[0] == parts[1] {
				// Skip useless identity mapping.
				continue
			}
			authHandlerNames[parts[0]] = parts[1]
		} else {
			return errors.New("store: invalid logical auth mapping '" + pair + "'")
		}
	}
	return nil
}

// Registered authentication handlers.
var validators map[string]validate.Validator

// RegisterValidator registers validation scheme.
func RegisterValidator(name string, v validate.Validator) {
	name = strings.ToLower(name)
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

// GetValidator returns registered validator by name.
func (storeObj) GetValidator(name string) validate.Validator {
	return validators[strings.ToLower(name)]
}

// DevicePersistenceInterface is an interface which defines methods used for handling device IDs.
// Mostly used to generate push notifications.
type DevicePersistenceInterface interface {
	Update(uid types.Uid, oldDeviceID string, dev *types.DeviceDef) error
	GetAll(uid ...types.Uid) (map[types.Uid][]types.DeviceDef, int, error)
	Delete(uid types.Uid, deviceID string) error
}

// deviceMapper is a concrete type implementing DevicePersistenceInterface.
type deviceMapper struct{}

// Devices is a singleton instance of DevicePersistenceInterface to map methods to.
var Devices DevicePersistenceInterface

// Update updates a device record.
func (deviceMapper) Update(uid types.Uid, oldDeviceID string, dev *types.DeviceDef) error {
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

// GetAll returns all known device IDs for a given list of user IDs.
// The second return parameter is the count of found device IDs.
func (deviceMapper) GetAll(uid ...types.Uid) (map[types.Uid][]types.DeviceDef, int, error) {
	return adp.DeviceGetAll(uid...)
}

// Delete deletes device record for a given user.
func (deviceMapper) Delete(uid types.Uid, deviceID string) error {
	return adp.DeviceDelete(uid, deviceID)
}

// Registered media/file handlers.
var fileHandlers map[string]media.Handler

// RegisterMediaHandler saves reference to a media handler (file upload-download handler).
func RegisterMediaHandler(name string, mh media.Handler) {
	if fileHandlers == nil {
		fileHandlers = make(map[string]media.Handler)
	}

	if mh == nil {
		panic("RegisterMediaHandler: handler is nil")
	}
	if _, dup := fileHandlers[name]; dup {
		panic("RegisterMediaHandler: called twice for handler " + name)
	}
	fileHandlers[name] = mh
}

// GetMediaHandler returns default media handler.
func (storeObj) GetMediaHandler() media.Handler {
	return mediaHandler
}

// UseMediaHandler sets specified media handler as default.
func (storeObj) UseMediaHandler(name, config string) error {
	mediaHandler = fileHandlers[name]
	if mediaHandler == nil {
		panic("UseMediaHandler: unknown handler '" + name + "'")
	}
	return mediaHandler.Init(config)
}

// FilePersistenceInterface is an interface wchich defines methods used for file handling (records or uploaded files).
type FilePersistenceInterface interface {
	// StartUpload records that the given user initiated a file upload
	StartUpload(fd *types.FileDef) error
	// FinishUpload marks started upload as successfully finished.
	FinishUpload(fd *types.FileDef, success bool, size int64) (*types.FileDef, error)
	// Get fetches a file record for a unique file id.
	Get(fid string) (*types.FileDef, error)
	// DeleteUnused removes unused attachments.
	DeleteUnused(olderThan time.Time, limit int) error
	// LinkAttachments connects earlier uploaded attachments to a message or topic to prevent it
	// from being garbage collected.
	LinkAttachments(topic string, msgId types.Uid, attachments []string) error
}

// fileMapper is concrete type which implements FilePersistenceInterface.
type fileMapper struct{}

// Files is a sigleton instance of FilePersistenceInterface to be used for handling file uploads.
var Files FilePersistenceInterface

// StartUpload records that the given user initiated a file upload
func (fileMapper) StartUpload(fd *types.FileDef) error {
	fd.Status = types.UploadStarted
	return adp.FileStartUpload(fd)
}

// FinishUpload marks started upload as successfully finished or failed.
func (fileMapper) FinishUpload(fd *types.FileDef, success bool, size int64) (*types.FileDef, error) {
	return adp.FileFinishUpload(fd, success, size)
}

// Get fetches a file record for a unique file id.
func (fileMapper) Get(fid string) (*types.FileDef, error) {
	return adp.FileGet(fid)
}

// DeleteUnused removes unused attachments and avatars.
func (fileMapper) DeleteUnused(olderThan time.Time, limit int) error {
	toDel, err := adp.FileDeleteUnused(olderThan, limit)
	if err != nil {
		return err
	}
	if len(toDel) > 0 {
		logs.Warn.Println("deleting media", toDel)
		return Store.GetMediaHandler().Delete(toDel)
	}
	return nil
}

// LinkAttachments connects earlier uploaded attachments to a message or topic to prevent it
// from being garbage collected.
func (fileMapper) LinkAttachments(topic string, msgId types.Uid, attachments []string) error {
	// Convert attachment URLs to file IDs.
	var fids []string
	for _, url := range attachments {
		if fid := mediaHandler.GetIdFromUrl(url); !fid.IsZero() {
			fids = append(fids, fid.String())
		}
	}

	if len(fids) > 0 {
		userId := types.ZeroUid
		if types.GetTopicCat(topic) == types.TopicCatMe {
			userId = types.ParseUserId(topic)
			topic = ""
		}
		return adp.FileLinkAttachments(topic, userId, msgId, fids)
	}
	return nil
}

func init() {
	Store = storeObj{}
	Users = usersMapper{}
	Topics = topicsMapper{}
	Subs = subsMapper{}
	Messages = messagesMapper{}
	Devices = deviceMapper{}
	Files = fileMapper{}
}
