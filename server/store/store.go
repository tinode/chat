/******************************************************************************
 *
 *  Copyright (C) 2014-2016 Tinode, All Rights Reserved
 *
 *  This program is free software; you can redistribute it and/or modify it
 *  under the terms of the GNU Affero General Public License as published by
 *  the Free Software Foundation; either version 3 of the License, or (at your
 *  option) any later version.
 *
 *  This program is distributed in the hope that it will be useful, but
 *  WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY
 *  or FITNESS FOR A PARTICULAR PURPOSE.
 *  See the GNU Affero General Public License for more details.
 *
 *  You should have received a copy of the GNU Affero General Public License
 *  along with this program; if not, see <http://www.gnu.org/licenses>.
 *
 *  This code is available under licenses for commercial use.
 *
 *  File        :  store.go
 *
 ******************************************************************************
 *
 *  Description :
 *
 *  Database abastraction layer
 *
 *****************************************************************************/

package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/adapter"
	"github.com/tinode/chat/server/store/types"
)

const (
	MAX_USERS_FOR_TOPIC = 32
)

var adaptr adapter.Adapter

// Unique ID generator
var uGen types.UidGenerator

type configType struct {
	// The following two values ate used to initialize types.UidGenerator
	// Snowflake workerId, beteween 0 and 1023
	WorkerID int `json:"worker_id"`
	// 16-byte key for XTEA
	UidKey        []byte          `json:"uid_key"`
	AdapterConfig json.RawMessage `json:"adapter_config"`
}

// Open initializes the persistence system. Adapter holds a connection pool for a single database.
//   name - the name of adapter to use
//   jsonconf - configuration string
func Open(name, jsonconf string) error {
	if adaptr == nil {
		return errors.New("store: attept to Open an adapter before registering")
	}
	if adaptr.IsOpen() {
		return errors.New("store: connection is already opened")
	}

	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("store: failed to parse config: " + err.Error() + "(" + jsonconf + ")")
	}

	// Initialise snowflake
	if err := uGen.Init(uint(config.WorkerID), config.UidKey); err != nil {
		return errors.New("store: failed to init snowflake: " + err.Error())
	}

	return adaptr.Open(string(config.AdapterConfig))
}

func Close() error {
	if adaptr.IsOpen() {
		return adaptr.Close()
	} else {
		return errors.New("store: connection already closed")
	}
}

func IsOpen() bool {
	if adaptr != nil {
		return adaptr.IsOpen()
	} else {
		return false
	}
}

func InitDb(reset bool) error {
	return adaptr.CreateDb(reset)
}

// Register makes a persistence adapter available by the provided name.
// If Register is called twice with the same name or if the adapter is nil,
// it panics.
// Name is currently unused, i.e. only a single adapter can be registered
func Register(name string, adapter adapter.Adapter) {
	if adapter == nil {
		panic("store: Register adapter is nil")
	}
	if adaptr != nil {
		panic("store: Adapter already registered")
	}
	adaptr = adapter
}

// Generate unique ID
func GetUid() types.Uid {
	return uGen.Get()
}

// Generate unique ID as string
func GetUidString() string {
	return uGen.GetStr()
}

// Users struct to hold methods for persistence mapping for the User object.
type UsersObjMapper struct{}

// Users is the ancor for storing/retrieving User objects
var Users UsersObjMapper

// CreateUser inserts User object into a database, updates creation time and assigns UID
func (u UsersObjMapper) Create(user *types.User, private interface{}) (*types.User, error) {

	user.SetUid(GetUid())
	user.InitTimes()

	err, _ := adaptr.UserCreate(user)
	if err != nil {
		return nil, err
	}

	// Create user's subscription to 'me' && 'find'. Theese topics are ephemeral, the topic object need not to be inserted.
	err = Subs.Create(
		&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
			User:      user.Id,
			Topic:     user.Uid().UserId(),
			ModeWant:  types.ModeSelf,
			ModeGiven: types.ModeSelf,
			Private:   private,
		},
		&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
			User:      user.Id,
			Topic:     user.Uid().FndName(),
			ModeWant:  types.ModeSelf,
			ModeGiven: types.ModeSelf,
			Private:   nil,
		})
	if err != nil {
		// Best effort to delete incomplete user record. Orphaned user records are not a problem.
		// They just take up space.
		adaptr.UserDelete(user.Uid(), true)
		return nil, err
	}

	return user, nil
}

// Given a unique identifier and a authentication scheme name, fetch user ID and authentication secret
func (UsersObjMapper) GetAuthRecord(scheme, unique string) (types.Uid, []byte, time.Time, error) {
	return adaptr.GetAuthRecord(scheme + ":" + unique)
}

// Create a new authentication record for user
func (UsersObjMapper) AddAuthRecord(uid types.Uid, scheme, unique string, secret []byte, expires time.Time) (error, bool) {
	return adaptr.AddAuthRecord(uid, scheme+":"+unique, secret, expires)
}

// Update authentication record with a new secret and expiration time
func (UsersObjMapper) UpdateAuthRecord(uid types.Uid, scheme, unique string, secret []byte, expires time.Time) (int, error) {
	return adaptr.UpdAuthRecord(scheme+":"+unique, secret, expires)
}

// Get returns a user object for the given user id
func (UsersObjMapper) Get(uid types.Uid) (*types.User, error) {
	return adaptr.UserGet(uid)
}

// GetAll returns a slice of user objects for the given user ids
func (UsersObjMapper) GetAll(uid ...types.Uid) ([]types.User, error) {
	return adaptr.UserGetAll(uid...)
}

// TODO(gene): implement
func (UsersObjMapper) Delete(id types.Uid, soft bool) error {
	return errors.New("store: not implemented")
}

func (UsersObjMapper) UpdateStatus(id types.Uid, status interface{}) error {
	return errors.New("store: not implemented")
}

func (UsersObjMapper) UpdateLastSeen(uid types.Uid, userAgent string, when time.Time) error {
	return adaptr.UserUpdateLastSeen(uid, userAgent, when)
}

func (UsersObjMapper) Update(uid types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.UserUpdate(uid, update)
}

// GetSubs loads a list of subscriptions for the given user
func (u UsersObjMapper) GetSubs(id types.Uid) ([]types.Subscription, error) {
	return adaptr.SubsForUser(id)
}

// GetSubs loads a list of subscriptions for the given user
func (u UsersObjMapper) FindSubs(id types.Uid, query []interface{}) ([]types.Subscription, error) {
	return adaptr.FindSubs(id, query)
}

// GetTopics is exacly the same as Topics.GetForUser
func (u UsersObjMapper) GetTopics(id types.Uid) ([]types.Subscription, error) {
	return adaptr.TopicsForUser(id)
}

// Topics struct to hold methods for persistence mapping for the topic object.
type TopicsObjMapper struct{}

var Topics TopicsObjMapper

// Create creates a topic and owner's subscription to topic
func (TopicsObjMapper) Create(topic *types.Topic, owner types.Uid, private interface{}) error {

	topic.InitTimes()

	err := adaptr.TopicCreate(topic)
	if err != nil {
		return err
	}

	if !owner.IsZero() {
		err = Subs.Create(&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: topic.CreatedAt},
			User:      owner.String(),
			Topic:     topic.Id,
			ModeGiven: types.ModeFull,
			ModeWant:  topic.GetAccess(owner),
			Private:   private})
	}

	return err
}

// CreateP2P creates a P2P topic by generating two user's subsciptions to each other.
func (TopicsObjMapper) CreateP2P(initiator, invited *types.Subscription) error {
	initiator.InitTimes()
	invited.InitTimes()

	return adaptr.TopicCreateP2P(initiator, invited)
}

// Get a single topic with a list of relevent users de-normalized into it
func (TopicsObjMapper) Get(topic string) (*types.Topic, error) {
	return adaptr.TopicGet(topic)
}

// GetUsers loads subscriptions for topic plus loads user.Public
func (TopicsObjMapper) GetUsers(topic string) ([]types.Subscription, error) {
	return adaptr.UsersForTopic(topic)
}

// GetSubs loads a list of subscriptions to the given topic, user.Public is not loaded
func (TopicsObjMapper) GetSubs(topic string) ([]types.Subscription, error) {
	return adaptr.SubsForTopic(topic)
}

func (TopicsObjMapper) Update(topic string, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.TopicUpdate(topic, update)
}

func (TopicsObjMapper) Delete(topic string) error {
	if err := adaptr.SubsDelForTopic(topic); err != nil {
		return err
	}

	if err := adaptr.TopicDelete(topic); err != nil {
		return err
	}

	return adaptr.MessageDeleteAll(topic, -1)
}

// Topics struct to hold methods for persistence mapping for the topic object.
type SubsObjMapper struct{}

var Subs SubsObjMapper

func (SubsObjMapper) Create(subs ...*types.Subscription) error {
	for _, sub := range subs {
		sub.InitTimes()
	}

	_, err := adaptr.TopicShare(subs)
	return err
}

func (SubsObjMapper) Get(topic string, user types.Uid) (*types.Subscription, error) {
	return adaptr.SubscriptionGet(topic, user)
}

// Update changes values of user's subscription.
func (SubsObjMapper) Update(topic string, user types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.SubsUpdate(topic, user, update)
}

// Delete deletes a subscription
func (SubsObjMapper) Delete(topic string, user types.Uid) error {
	return adaptr.SubsDelete(topic, user)
}

// Messages struct to hold methods for persistence mapping for the Message object.
type MessagesObjMapper struct{}

var Messages MessagesObjMapper

// Save message
func (MessagesObjMapper) Save(msg *types.Message) error {
	msg.InitTimes()

	// Need a transaction here, RethinkDB does not support transactions

	// An invite (message to 'me') may have a zero SeqId if 'me' was inactive at the time of generating the invite
	if msg.SeqId == 0 {
		if user, err := adaptr.UserGet(types.ParseUserId(msg.Topic)); err != nil {
			return err
		} else {
			msg.SeqId = user.SeqId + 1
		}
	}

	if err := adaptr.TopicUpdateOnMessage(msg.Topic, msg); err != nil {
		return err
	}

	return adaptr.MessageSave(msg)
}

// Delete messages. Hard-delete if hard==tru, otherwise a soft-delete
// If hard == true:
// If topic == "", it's a hard delete for 'me' topic of forUser
// Otherwise it's a hard-delete in 'topic' for all users
func (MessagesObjMapper) Delete(topic string, forUser types.Uid, hard bool, cleared int) (err error) {
	if hard {
		err = adaptr.MessageDeleteAll(topic, cleared)
		if err != nil {
			update := map[string]interface{}{"ClearId": cleared}
			if topic == "" {
				err = adaptr.UserUpdate(forUser, update)
			} else {
				err = adaptr.TopicUpdate(topic, update)
			}
		}
	} else {
		update := map[string]interface{}{"ClearId": cleared}
		err = adaptr.SubsUpdate(topic, forUser, update)
	}

	return
}

func (MessagesObjMapper) GetAll(topic string, opt *types.BrowseOpt) ([]types.Message, error) {
	return adaptr.MessageGetAll(topic, opt)
}

var authHandlers map[string]auth.AuthHandler

// Register an authentication scheme handler
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

func GetAuthHandler(name string) auth.AuthHandler {
	return authHandlers[name]
}

// Storage for device IDs, used to generate push notifications
type DeviceMapper struct{}

var Devices DeviceMapper

func (DeviceMapper) Update(uid types.Uid, dev *types.DeviceDef) error {
	return adaptr.DeviceUpsert(uid, dev)
}

func (DeviceMapper) GetAll(uid ...types.Uid) (map[types.Uid][]types.DeviceDef, int, error) {
	return adaptr.DeviceGetAll(uid...)
}

func (DeviceMapper) Delete(uid types.Uid, deviceId string) error {
	return adaptr.DeviceDelete(uid, deviceId)
}
