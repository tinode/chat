package push_fcm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/tinode/chat/server/push"
	"github.com/tinode/fcm"
)

var handler FcmPush

const DEFAULT_BUFFER = 32

type FcmPush struct {
	input  chan *push.Receipt
	stop   chan bool
	client *fcm.Client
}

type configType struct {
	Disabled bool `json:"disabled"`
	Buffer   int  `json:"buffer"`
	ApiKey   int  `json:"api_key"`
}

// Initialize the handler
func (FcmPush) Init(jsonconf string) error {

	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if config.Disabled {
		return nil
	}

	handler.client = fcm.NewClient(config.ApiKey)

	if config.Buffer <= 0 {
		config.Buffer = DEFAULT_BUFFER
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				msg := &fcm.HttpMessage{}
				go handler.client.SendHttp(msg)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

// Initialize the handler
func (FcmPush) IsReady() bool {
	return handler.input != nil
}

// Push return a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (FcmPush) Push() chan<- *push.Receipt {
	return handler.input
}

func (FcmPush) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("fcm", &handler)
}
