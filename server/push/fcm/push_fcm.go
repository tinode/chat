package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"

	fbase "firebase.google.com/go"
	fcm "firebase.google.com/go/messaging"

	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"

	"google.golang.org/api/option"
)

var handler Handler

// Size of the input channel buffer.
const defaultBuffer = 32

// Handler represents the push handler; implements push.PushHandler interface.
type Handler struct {
	input  chan *push.Receipt
	stop   chan bool
	client *fcm.Client
}

type configType struct {
	Enabled     bool   `json:"enabled"`
	ProjectID   string `json:"project_id"`
	Buffer      int    `json:"buffer"`
	APIKey      string `json:"api_key"`
	TimeToLive  uint   `json:"time_to_live,omitempty"`
	CollapseKey string `json:"collapse_key,omitempty"`
	Icon        string `json:"icon,omitempty"`
	IconColor   string `json:"icon_color,omitempty"`
}

// Init initializes the push handler
func (Handler) Init(jsonconf string) error {

	var config configType
	err := json.Unmarshal([]byte(jsonconf), &config)
	if err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return nil
	}

	app, err := fbase.NewApp(context.Background(),
		&fbase.Config{ProjectID: config.ProjectID},
		option.WithAPIKey(config.APIKey))
	if err != nil {
		return err
	}

	handler.client, err = app.Messaging(context.Background())
	if err != nil {
		return err
	}

	if config.Buffer <= 0 {
		config.Buffer = defaultBuffer
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendNotifications(rcpt, &config)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

func payloadToData(pl *push.Payload) map[string]string {
	if pl == nil {
		return nil
	}

	data := make(map[string]string)
	data["topic"] = pl.Topic
	data["from"] = pl.From
	data["ts"] = pl.Timestamp.String()
	data["seq"] = strconv.Itoa(pl.SeqId)
	data["mime"] = pl.ContentType
	data["content"], _ = push.DraftyToPlainText(pl.Content)
	return nil
}

func sendNotifications(rcpt *push.Receipt, config *configType) {
	ctx := context.Background()

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

	data := payloadToData(&rcpt.Payload)

	for uid, devList := range devices {
		for i := range devList {
			d := &devList[i]
			if _, ok := skipDevices[d.DeviceId]; !ok && d.DeviceId != "" {
				msg := fcm.Message{
					Token: d.DeviceId,
					Data:  data,
					Notification: &fcm.Notification{
						Title: "You received a message",
						Body:  data["content"],
					},
				}

				_, err = handler.client.Send(ctx, &msg)
				if err != nil {
					if fcm.IsRegistrationTokenNotRegistered(err) {
						// Token is no longer valid.
						store.Devices.Delete(uid, d.DeviceId)
					} else if fcm.IsMessageRateExceeded(err) ||
						fcm.IsServerUnavailable(err) ||
						fcm.IsInternal(err) ||
						fcm.IsUnknown(err) {
						// Transient errors. Stop sending this batch.
						log.Println("fcm transient error", err)
						return
					} else if fcm.IsMismatchedCredential(err) || fcm.IsInvalidArgument(err) {
						// Config errors
						log.Println("fcm push failed", err)
						return
					}
				}
			}
		}
	}

	/*
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
	*/
}

// IsReady checks if the push handler has been initialized.
func (Handler) IsReady() bool {
	return handler.input != nil
}

// Push return a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (Handler) Push() chan<- *push.Receipt {
	return handler.input
}

// Stop shuts down the handler
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("fcm", &handler)
}
