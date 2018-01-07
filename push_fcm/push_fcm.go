package push_fcm

import (
	"encoding/json"
	"errors"

	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	"github.com/tinode/fcm"
)

var handler FcmPush

const DEFAULT_BUFFER = 32

// FcmPush represents the push handler; implements push.PushHandler interface.
type FcmPush struct {
	input  chan *push.Receipt
	stop   chan bool
	client *fcm.Client
}

type configType struct {
	Disabled    bool   `json:"disabled"`
	Buffer      int    `json:"buffer"`
	APIKey      string `json:"api_key"`
	TimeToLive  uint   `json:"time_to_live,omitempty"`
	CollapseKey string `json:"collapse_key,omitempty"`
	Icon        string `json:"icon,omitempty"`
	IconColor   string `json:"icon_color,omitempty"`
}

// Init initializes the push handler
func (FcmPush) Init(jsonconf string) error {

	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if config.Disabled {
		return nil
	}

	handler.client = fcm.NewClient(config.APIKey)

	if config.Buffer <= 0 {
		config.Buffer = DEFAULT_BUFFER
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendNotification(rcpt, &config)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

func sendNotification(rcpt *push.Receipt, config *configType) {
	// List of UIDs for querying the database
	uids := make([]t.Uid, len(rcpt.To))
	skipDevices := make(map[string]bool)
	for i, to := range rcpt.To {
		uids[i] = to.User

		// Some devices were online and received the message. Skip them.
		for _, deviceID := range to.Devices {
			skipDevices[deviceID] = true
		}
	}

	devices, count, err := store.Devices.GetAll(uids...)
	if err != nil || count == 0 {
		return
	}

	sendTo := make([]string, count)
	i := 0
	for _, devList := range devices {
		for _, d := range devList {
			if _, ok := skipDevices[d.DeviceId]; !ok {
				sendTo[i] = d.DeviceId
				i++
			}
		}
	}

	msg := &fcm.HttpMessage{
		To:               "",
		RegistrationIds:  sendTo,
		CollapseKey:      config.CollapseKey, // Optionally collapse several notification messages (i.e. "message sent")
		Priority:         fcm.PriorityHigh,   // These are IM messages, they are high priority
		ContentAvailable: true,               // to wake up the iOS app
		TimeToLive:       &config.TimeToLive,
		DryRun:           false,
		// FIXME(gene): the real plugin must understand the structure of data to
		// ensure it does not exceed 4KB. Messages on "me" are structured and must be converted to text first.
		Data: rcpt.Payload,
		Notification: &fcm.Notification{
			Title:        "X sent a message",
			Body:         "X sent a message",
			Sound:        "default",
			ClickAction:  "",
			BodyLocKey:   "",
			BodyLocArgs:  "",
			TitleLocKey:  "",
			TitleLocArgs: "",

			// Android only
			Icon:  config.Icon,
			Tag:   "", // use some tag for coalesing notifications
			Color: config.IconColor,

			// iOS only
			Badge: "",
		},
	}

	resp, err := handler.client.SendHttp(msg)
	if err != nil {
		return
	}

	if resp.Fail > 0 {
		// Generate an inverse index to speed up processing
		devIds := make(map[string]t.Uid)
		for uid, devList := range devices {
			for _, d := range devList {
				devIds[d.DeviceId] = uid
			}
		}

		i = 0
		for _, fail := range resp.Results {
			switch fail.Error {
			case fcm.ErrorInvalidRegistration,
				fcm.ErrorNotRegistered,
				fcm.ErrorMismatchSenderId:
				if uid, ok := devIds[sendTo[i]]; ok {
					store.Devices.Delete(uid, sendTo[i])
					// log.Printf("FCM push: %s; token removed: %s", fail.Error, sendTo[i])
				}
			}
			i++
		}
	}

}

// IsReady checks if the push handler has been initialized.
func (FcmPush) IsReady() bool {
	return handler.input != nil
}

// Push return a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (FcmPush) Push() chan<- *push.Receipt {
	return handler.input
}

// Stop shuts down the handler
func (FcmPush) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("fcm", &handler)
}
