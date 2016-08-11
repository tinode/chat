package push

// Interfaces for push notifications

import (
	"time"
)

type PushTo struct {
	// Addresse
	Uid string `json:"uid"`
	// Count of user's connections that were live when the packet was dispatched from the server
	Delieved int `json:"delivered"`
	// List of user's devices that the packet was delivered to (if known)
	Devices []string `json:"devices,omitempty"`
}

type Receipt struct {
	Topic     string    `json:"topic"`
	From      string    `json:"from"`
	Timestamp time.Time `json:"ts"`
	SeqId     int       `json:"seq"`
	// List of recepients, including those who did not receive the message
	To []PushTo `json:"to"`
	// Actual Data.Content of the message, if requested
	Data interface{} `json:"data,omitempty"`
}

type PushHandler interface {
	// Initialize the handler
	Init(jsonconf string) error

	// Push return a channel that the server will use to send messages to.
	// If the adapter blocks, the message will be dropped.
	Push() chan<- *Receipt

	// Stop operations
	Stop()
}

var handlers map[string]PushHandler

// Register a push handler
func Register(name string, hnd PushHandler) {
	if handlers == nil {
		handlers = make(map[string]PushHandler)
	}

	if hnd == nil {
		panic("Register: push handler is nil")
	}
	if _, dup := handlers[name]; dup {
		panic("Register: called twice for handler " + name)
	}
	handlers[name] = hnd
}

func Push(msg *Receipt) {
	if handlers == nil {
		return
	}

	for _, hnd := range handlers {
		// Push without delay or skip
		select {
		case hnd.Push() <- msg:
		default:
		}
	}
}

func Stop() {
	if handlers == nil {
		return
	}

	for _, hnd := range handlers {
		hnd.Stop()
	}
}
