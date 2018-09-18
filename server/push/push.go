package push

// Interfaces for push notifications

import (
	"encoding/json"
	"errors"
	"time"

	t "github.com/tinode/chat/server/store/types"
)

// Recipient is a user targeted by the push.
type Recipient struct {
	// Addressee
	User t.Uid `json:"user"`
	// Count of user's connections that were live when the packet was dispatched from the server
	Delivered int `json:"delivered"`
	// List of user's devices that the packet was delivered to (if known). Len(Devices) >= Delivered
	Devices []string `json:"devices,omitempty"`
}

// Receipt is the push payload with a list of recipients.
type Receipt struct {
	// List of recipients, including those who did not receive the message
	To []Recipient `json:"to"`
	// Actual content to be delivered to the client
	Payload Payload `json:"payload"`
}

// Payload is content of the push.
type Payload struct {
	Topic       string    `json:"topic"`
	From        string    `json:"from"`
	Timestamp   time.Time `json:"ts"`
	SeqId       int       `json:"seq"`
	ContentType string    `json:"mime"`
	// Actual Data.Content of the message, if requested
	Content interface{} `json:"content,omitempty"`
}

// Handler is an interface which must be implemented by handlers.
type Handler interface {
	// Initialize the handler
	Init(jsonconf string) error

	// Check if the handler is initialized
	IsReady() bool

	// Push returns a channel that the server will use to send messages to.
	// The message will be dropped if the channel blocks.
	Push() chan<- *Receipt

	// Stop operations
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
func Init(jsconfig string) error {
	var config []configType

	if err := json.Unmarshal([]byte(jsconfig), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	for _, cc := range config {
		if hnd := handlers[cc.Name]; hnd != nil {
			if err := hnd.Init(string(cc.Config)); err != nil {
				return err
			}
		}
	}

	return nil
}

// Push a single message
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
