// Package stdout is a sample implementation of a push plugin.
// If enabled, it writes every notification to stdout.
package stdout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/tinode/chat/server/push"
)

var handler stdoutPush

// How much to buffer the input channel.
const defaultBuffer = 32

type stdoutPush struct {
	initialized bool
	input       chan *push.Receipt
	channel     chan *push.ChannelReq
	stop        chan bool
}

type configType struct {
	Enabled bool `json:"enabled"`
	Buffer  int  `json:"buffer"`
}

// Init initializes the handler
func (stdoutPush) Init(jsonconf json.RawMessage) (bool, error) {

	// Check if the handler is already initialized
	if handler.initialized {
		return false, errors.New("already initialized")
	}

	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return false, errors.New("failed to parse config: " + err.Error())
	}

	handler.initialized = true

	if !config.Enabled {
		return false, nil
	}

	if config.Buffer <= 0 {
		config.Buffer = defaultBuffer
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.channel = make(chan *push.ChannelReq, config.Buffer)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case msg := <-handler.input:
				fmt.Fprintln(os.Stdout, msg)
			case msg := <-handler.channel:
				fmt.Fprintln(os.Stdout, msg)
			case <-handler.stop:
				return
			}
		}
	}()

	return true, nil
}

// IsReady checks if the handler is initialized.
func (stdoutPush) IsReady() bool {
	return handler.input != nil
}

// Push returns a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (stdoutPush) Push() chan<- *push.Receipt {
	return handler.input
}

// Channel returns a channel that caller can use to subscribe/unsubscribe devices to channels (FCM topics).
// If the adapter blocks, the message will be dropped.
func (stdoutPush) Channel() chan<- *push.ChannelReq {
	return handler.channel
}

// Stop terminates the handler's worker and stops sending pushes.
func (stdoutPush) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("stdout", &handler)
}
