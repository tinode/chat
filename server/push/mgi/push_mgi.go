package mgi

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mgi-vn/common/pkg/constants"
	"github.com/tinode/chat/server/drafty"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/queue"
	"github.com/tinode/chat/server/store"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	commonSchemas "github.com/mgi-vn/common/pkg/schemas"
	"github.com/tinode/chat/server/push"
)

var handler Handler

const (
	defaultBuffer = 32
	NotificationExchangeName = "notification_exchange"
	NotificationMulticastKey = "notification.multicast"
)


type Handler struct {
	initialized bool
	input       chan *push.Receipt
	channel     chan *push.ChannelReq
	stop        chan bool
	rabbitMQ *queue.RabbitMQ
}

type configType struct {
	Enabled bool `json:"enabled"`
	Buffer  int  `json:"buffer"`
}

// Init initializes the handler
func (Handler) Init(jsonconf string) error {

	// Check if the handler is already initialized
	if handler.initialized {
		return errors.New("already initialized")
	}

	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	handler.initialized = true

	if !config.Enabled {
		return nil
	}

	if config.Buffer <= 0 {
		config.Buffer = defaultBuffer
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.channel = make(chan *push.ChannelReq, config.Buffer)
	handler.stop = make(chan bool, 1)
	rabbitMQ := queue.NewRabbitMQ()
	rabbitMQ.Init()
	handler.rabbitMQ = rabbitMQ

	go func() {
		for {
			select {
			case msg := <-handler.input:
				sendNotifications(msg, rabbitMQ)
			case msg := <-handler.channel:
				//subscription method not supported
				fmt.Fprintln(os.Stdout, msg)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

func sendNotifications(rcpt *push.Receipt, rabbitMQ *queue.RabbitMQ) {
	fmt.Println("sendNotifications",rcpt.Payload)
	data, err := payloadToData(&rcpt.Payload)
	if err != nil {
		logs.Warn.Println("fcm push: could not parse payload;", err)
		return
	}
	fmt.Println(data)
	if len(rcpt.To) > 0 {
		// List of UIDs for querying the database
		uids := make([]string, len(rcpt.To))
		i := 0
		for uid, _ := range rcpt.To {
			user, _ := store.Users.Get(uid)
			fmt.Println("user", user.Tags)
			userId := ""
			for _, tag := range user.Tags {
				if strings.Contains(tag, "token:") {
					userId = strings.Replace(tag, "token:", "", -1)
					break
				}
			}
			if userId == "" {
				fmt.Println("Can not find user id")
			}
			fmt.Println("userId", userId)
			uids[i] = userId
			i++
		}
		fmt.Println("uids", uids)

		var title string
		action := constants.NotificationActionNewChatMessage
		if rcpt.Payload.What == push.ActMsg {
			title = "Bạn nhận được tin nhắn mới"
		} else if rcpt.Payload.What == push.ActSub {
			title = "Bạn có một cuộc trò chuyện mới"
			action = constants.NotificationActionNewChatTopic
		}
		if title == "" {
			title = "Bạn nhận được tin nhắn mới"
		}


		notify := &commonSchemas.NotificationData{
			Recipients: uids,
			Type:       constants.NotificationTypePush,
			Action:     action,
			Payload: &commonSchemas.NotificationPayload{
				Title:   title,
				Message: data["content"],
				Data: map[string]string{
					"topic_id":     rcpt.Payload.Topic,
				},
			},
		}

		notifyByte, err := json.Marshal(notify)
		if err != nil {
			log.Println(err)
		}
		rabbitMQ.PushRawMessage(NotificationExchangeName, NotificationMulticastKey, notifyByte)
	}

}

func payloadToData(pl *push.Payload) (map[string]string, error) {
	if pl == nil {
		return nil, errors.New("empty push payload")
	}
	data := make(map[string]string)
	var err error
	data["what"] = pl.What
	if pl.Silent {
		data["silent"] = "true"
	}
	data["topic"] = pl.Topic
	data["ts"] = pl.Timestamp.Format(time.RFC3339Nano)
	// Must use "xfrom" because "from" is a reserved word. Google did not bother to document it anywhere.
	data["xfrom"] = pl.From
	if pl.What == push.ActMsg {
		data["seq"] = strconv.Itoa(pl.SeqId)
		data["mime"] = pl.ContentType

		// Convert Drafty content to plain text (clients 0.16 and below).
		data["content"], err = drafty.ToPlainText(pl.Content)
		if err != nil {
			return nil, err
		}
		// Trim long strings to 128 runes.
		// Check byte length first and don't waste time converting short strings.
		if len(data["content"]) > push.MaxPayloadLength {
			runes := []rune(data["content"])
			if len(runes) > push.MaxPayloadLength {
				data["content"] = string(runes[:push.MaxPayloadLength]) + "…"
			}
		}

		// Rich content for clients version 0.17 and above.
		data["rc"], err = drafty.Preview(pl.Content, push.MaxPayloadLength)
		if err != nil {
			return nil, err
		}
	} else if pl.What == push.ActSub {
		data["modeWant"] = pl.ModeWant.String()
		data["modeGiven"] = pl.ModeGiven.String()
	} else {
		return nil, errors.New("unknown push type")
	}
	return data, nil
}


// IsReady checks if the handler is initialized.
func (Handler) IsReady() bool {
	return handler.input != nil
}

// Push returns a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (Handler) Push() chan<- *push.Receipt {
	return handler.input
}

// Channel returns a channel that caller can use to subscribe/unsubscribe devices to channels (FCM topics).
// If the adapter blocks, the message will be dropped.
func (Handler) Channel() chan<- *push.ChannelReq {
	return handler.channel
}

// Stop terminates the handler's worker and stops sending pushes.
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("mgi", &handler)
}
