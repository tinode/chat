package push_stdout

import (
	"fmt"
	"os"

	"github.com/tinode/chat/server/push"
)

var handler StdoutPush

type StdoutPush struct {
	input chan *push.Receipt
	stop  chan bool
}

// Initialize the handler
func (StdoutPush) Init(jsonconf string) error {
	handler.input = make(chan *push.Receipt, 32)
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
