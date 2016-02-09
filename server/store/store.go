/******************************************************************************
 *
 *  Copyright (C) 2014-2015 Tinode, All Rights Reserved
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
 *  Author      :  Gene Sokolov
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
	"strings"
	"time"

	"github.com/tinode/chat/server/store/adapter"
	"github.com/tinode/chat/server/store/types"
	"golang.org/x/crypto/bcrypt"
)

const (
	MAX_USERS_FOR_TOPIC = 32
)

var adaptr adapter.Adapter

type configType struct {
	// The following two values ate used to initialize types.UidGenerator
	// Snowflake workerId, beteween 0 and 1023
	WorkerID int `json:"worker_id"`
	// 16-byte key for XTEA
	UidKey []byte          `json:"uid_key"`
	Params json.RawMessage `json:"params"`
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

	return adaptr.Open(string(config.Params), config.WorkerID, config.UidKey)
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

// Users struct to hold methods for persistence mapping for the User object.
type UsersObjMapper struct{}

// Users is the ancor for storing/retrieving User objects
var Users UsersObjMapper

// CreateUser inserts User object into a database, updates creation time and assigns UID
func (u UsersObjMapper) Create(user *types.User, scheme, secret string, private interface{}) (*types.User, error) {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			user.InitTimes()

			user.Username = secret[:splitAt]
			var err error
			user.Passhash, err = bcrypt.GenerateFromPassword([]byte(secret[splitAt+1:]), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}

			// TODO(gene): maybe have some additional handling of duplicate user name error
			err, _ = adaptr.UserCreate(user)
			user.Passhash = nil
			if err != nil {
				return nil, err
			}

			// Create user's subscription to !me. The !me topic is ephemeral, the topic object need not to be inserted.
			err = Subs.Create(&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
				User:      user.Id,
				Topic:     user.Uid().UserId(),
				ModeWant:  types.ModeSelf,
				ModeGiven: types.ModeSelf,
				Private:   private,
			})
			if err != nil {
				return nil, err
			}

			return user, nil
		} else {
			return nil, errors.New("store: invalid format of secret")
		}
	}
	return nil, errors.New("store: unknown authentication scheme '" + scheme + "'")

}

// Process user login. TODO(gene): abstract out the authentication scheme
func (UsersObjMapper) Login(scheme, secret string) (types.Uid, error) {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			uname := strings.ToLower(secret[:splitAt])
			password := secret[splitAt+1:]

			uid, hash, err := adaptr.GetPasswordHash(uname)
			if err != nil {
				return types.ZeroUid, err
			} else if uid.IsZero() {
				// Invalid login
				return types.ZeroUid, nil
			}

			err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
			if err != nil {
				// Invalid password
				return types.ZeroUid, nil
			}
			//log.Println("Logged in as", uid, uid.String())
			return uid, nil
		} else {
			return types.ZeroUid, errors.New("store: invalid format of secret")
		}
	}
	return types.ZeroUid, errors.New("store: unknown authentication scheme '" + scheme + "'")
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
func (UsersObjMapper) Find(params map[string]interface{}) ([]types.User, error) {
	return nil, errors.New("store: not implemented")
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

// ChangePassword changes user's password in "basic" authentication scheme
func (UsersObjMapper) ChangeAuthCredential(uid types.Uid, scheme, secret string) error {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			return adaptr.ChangePassword(uid, secret[splitAt+1:])
		}
		return errors.New("store: invalid format of secret")
	}
	return errors.New("store: unknown authentication scheme '" + scheme + "'")
}

func (UsersObjMapper) Update(uid types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.UserUpdate(uid, update)
}

// GetSubs loads a list of subscriptions for the given user
func (u UsersObjMapper) GetSubs(id types.Uid) ([]types.Subscription, error) {
	return adaptr.SubsForUser(id)
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
			Topic:     topic.Name,
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
	if err := adaptr.TopicDelete(topic); err != nil {
		return err
	}

	// TODO(gene): the following two operations are optional. If they fail, they leave garbage, but don't
	// affect anything
	if err := adaptr.SubsDelForTopic(topic); err != nil {
		return err
	}

	return adaptr.MessageDeleteAll(topic, -1)
}

// Topics struct to hold methods for persistence mapping for the topic object.
type SubsObjMapper struct{}

var Subs SubsObjMapper

func (SubsObjMapper) Create(sub *types.Subscription) error {
	sub.InitTimes()

	_, err := adaptr.TopicShare([]types.Subscription{*sub})
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
