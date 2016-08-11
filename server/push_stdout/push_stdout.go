package push_stdout

import (
	"fmt"
	"os"

	"github.com/tinode/chat/server/push"
)

var handler StdoutPush

const DEFAULT_BUFFER	32

type StdoutPush struct {
	input chan *push.Receipt
	stop  chan bool
}

type configType struct {
	Disabled bool `json:"disabled"`
	Buffer   int  `json:"buffer"`
}

// Initialize the handler
func (StdoutPush) Init(jsonconf string) error {

	var config configType

	if err := json.Unmarshal([]byte(jsconfig), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if config.Disabled {
		return nil
	}

	if config.Buffer <= 0 {
		config.Buffer = DEFAULT_BUFFER
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case msg := <-handler.input:
				fmt.Fprintln(os.Stdout, msg)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

// Initialize the handler
func (StdoutPush) IsReady() bool {
	return handler.input != nil
}

// Push return a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (StdoutPush) Push() chan<- *push.Receipt {
	return handler.input
}

func (StdoutPush) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("stdout", &handler)
}
