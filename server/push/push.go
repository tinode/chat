// Package push contains interfaces to be implemented by push notification plugins.
package push

import (
	"encoding/json"
	"errors"
	"time"

	t "github.com/tinode/chat/server/store/types"
)

// Push actions
const (
	// New message.
	ActMsg = "msg"
	// New subscription.
	ActSub = "sub"
	// Messages read: clear unread count.
	ActRead = "read"
)

// MaxPayloadLength is the maximum length of push payload in multibyte characters.
const MaxPayloadLength = 128

// Recipient is a user targeted by the push.
type Recipient struct {
	// Count of user's connections that were live when the packet was dispatched from the server
	Delivered int `json:"delivered"`
	// List of user's devices that the packet was delivered to (if known). Len(Devices) >= Delivered
	Devices []string `json:"devices,omitempty"`
	// Unread count to include in the push
	Unread int `json:"unread"`
	// Indicates whether unread counter in the cache should be incremented before sending the push.
	ShouldIncrementUnreadCountInCache bool `json:"-"`
}

// Receipt is the push payload with a list of recipients.
type Receipt struct {
	// List of individual recipients, including those who did not receive the message.
	To map[t.Uid]Recipient `json:"to"`
	// Push topic for group notifications.
	Channel string `json:"channel"`
	// Actual content to be delivered to the client.
	Payload Payload `json:"payload"`
}

// ChannelReq is a request to subscribe/unsubscribe device ID(s) to channel(s) (FCM topic).
// - If DeviceID is provided, it's subscribed/unsubscribed to all user's channels.
// - If Channel is provided, then all user's devices are subscribed/unsubscribed from the channel.
type ChannelReq struct {
	// Uid is the ID of the user making request.
	Uid t.Uid
	// DeviceID is the device-provided token in case a single device is being subscribed to all channels.
	DeviceID string
	// Channel to subscribe to or unsubscribe from.
	Channel string
	// Unsub is set to true to unsubscribe devices, otherwise subscribe them.
	Unsub bool
}

// Payload is content of the push.
type Payload struct {
	// Action type of the push: new message (msg), new subscription (sub), etc.
	What string `json:"what"`
	// If this is a silent push: perform action but do not show a notification to the user.
	Silent bool `json:"silent"`
	// Topic which was affected by the action.
	Topic string `json:"topic"`
	// Timestamp of the action.
	Timestamp time.Time `json:"ts"`

	// {data} notification.

	// Message sender 'usrXXX'
	From string `json:"from"`
	// Sequential ID of the message.
	SeqId int `json:"seq"`
	// MIME-Type of the message content, text/x-drafty or text/plain
	ContentType string `json:"mime"`
	// Actual Data.Content of the message, if requested
	Content interface{} `json:"content,omitempty"`
	// State of the video call (available in video call messages only).
	Webrtc string `json:"webrtc,omitempty"`
	// If call is audio-only (available only if Webrtc is present).
	AudioOnly bool `json:"aonly,omitempty"`
	// Seq id the message is supposed to replace.
	Replace string `json:"replace,omitempty"`

	// Subscription change notification.

	// New access mode when notifying of a subscription change.
	// ModeNone for both means the subscription is removed.
	ModeWant  t.AccessMode `json:"want,omitempty"`
	ModeGiven t.AccessMode `json:"given,omitempty"`
}

// Handler is an interface which must be implemented by handlers.
type Handler interface {
	// Init initializes the handler.
	Init(jsonconf json.RawMessage) (bool, error)

	// IsReady сhecks if the handler is initialized.
	IsReady() bool

	// Push returns a channel that the server will use to send messages to.
	// The message will be dropped if the channel blocks.
	Push() chan<- *Receipt

	// Subscribe/unsubscribe device from FCM topic (channel).
	Channel() chan<- *ChannelReq

	// Stop terminates the handler's worker and stops sending pushes.
	Stop()
}

type configType struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

var handlers map[string]Handler

// Register a push handler
func Register(name string, hnd Handler) {
	if handlers == nil {
		handlers = make(map[string]Handler)
	}

	if hnd == nil {
		panic("Register: push handler is nil")
	}
	if _, dup := handlers[name]; dup {
		panic("Register: called twice for handler " + name)
	}
	handlers[name] = hnd
}

// Init initializes registered handlers.
func Init(jsconfig json.RawMessage) ([]string, error) {
	var config []configType

	if err := json.Unmarshal(jsconfig, &config); err != nil {
		return nil, errors.New("failed to parse config: " + err.Error())
	}

	var enabled []string
	for _, cc := range config {
		if hnd := handlers[cc.Name]; hnd != nil {
			if ok, err := hnd.Init(cc.Config); err != nil {
				return nil, err
			} else if ok {
				enabled = append(enabled, cc.Name)
			}
		}
	}

	return enabled, nil
}

// Push a single message to devices.
func Push(msg *Receipt) {
	if handlers == nil {
		return
	}

	for _, hnd := range handlers {
		if !hnd.IsReady() {
			continue
		}

		// Push without delay or skip
		select {
		case hnd.Push() <- msg:
		default:
		}
	}
}

// ChannelSub handles a channel (FCM topic) subscription/unsubscription request.
func ChannelSub(msg *ChannelReq) {
	if handlers == nil {
		return
	}

	for _, hnd := range handlers {
		if !hnd.IsReady() {
			continue
		}

		// Send without delay or skip.
		select {
		case hnd.Channel() <- msg:
		default:
		}
	}
}

// Stop all pushes
func Stop() {
	if handlers == nil {
		return
	}

	for _, hnd := range handlers {
		if hnd.IsReady() {
			// Will potentially block
			hnd.Stop()
		}
	}
}
