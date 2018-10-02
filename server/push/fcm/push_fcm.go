package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"

	fbase "firebase.google.com/go"
	fcm "firebase.google.com/go/messaging"

	"github.com/tinode/chat/server/drafty"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"

	"golang.org/x/oauth2/google"
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
	Enabled         bool            `json:"enabled"`
	Buffer          int             `json:"buffer"`
	Credentials     json.RawMessage `json:"credentials"`
	CredentialsFile string          `json:"credentials_file"`
	TimeToLive      uint            `json:"time_to_live,omitempty"`
	Icon            string          `json:"icon,omitempty"`
	IconColor       string          `json:"icon_color,omitempty"`
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
	ctx := context.Background()

	var opt option.ClientOption
	if config.Credentials != nil {
		credentials, err := google.CredentialsFromJSON(ctx, config.Credentials,
			"https://www.googleapis.com/auth/firebase.messaging")
		if err != nil {
			return err
		}
		opt = option.WithCredentials(credentials)
	} else if config.CredentialsFile != "" {
		opt = option.WithCredentialsFile(config.CredentialsFile)
	} else {
		return errors.New("missing credentials")
	}

	app, err := fbase.NewApp(ctx, &fbase.Config{}, opt)
	if err != nil {
		return err
	}

	handler.client, err = app.Messaging(ctx)
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

func payloadToData(pl *push.Payload) (map[string]string, error) {
	if pl == nil {
		return nil, nil
	}

	data := make(map[string]string)
	var err error
	data["topic"] = pl.Topic
	// Must use "xfrom" because "from" is a reserved word.
	// Google did not bother to document it anywhere.
	data["xfrom"] = pl.From
	data["ts"] = pl.Timestamp.String()
	data["seq"] = strconv.Itoa(pl.SeqId)
	data["mime"] = pl.ContentType
	data["content"], err = drafty.ToPlainText(pl.Content)
	if err != nil {
		return nil, err
	}

	if len(data["content"]) > 128 {
		data["content"] = data["content"][:128]
	}

	return data, nil
}

func sendNotifications(rcpt *push.Receipt, config *configType) {
	ctx := context.Background()

	data, _ := payloadToData(&rcpt.Payload)
	if data == nil || data["content"] == "" {
		// Could not parse payload or empty payload.
		return
	}

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
	if err != nil {
		log.Println("fcm push db error", err)
		return
	}
	if count == 0 {
		return
	}

	for uid, devList := range devices {
		for i := range devList {
			d := &devList[i]
			if _, ok := skipDevices[d.DeviceId]; !ok && d.DeviceId != "" {
				msg := fcm.Message{
					Token: d.DeviceId,
					Data:  data,
				}
				if d.Platform == "android" {
					msg.Android = &fcm.AndroidConfig{
						Notification: &fcm.AndroidNotification{
							Title: "New message",
							Body:  data["content"],
							Icon:  config.Icon,
							Color: config.IconColor,
						},
					}
				} else {
					// No need to handle webpush separately.
					msg.Notification = &fcm.Notification{
						Title: "New message",
						Body:  data["content"],
					}
				}
				_, err := handler.client.Send(ctx, &msg)
				if err != nil {
					if fcm.IsMessageRateExceeded(err) ||
						fcm.IsServerUnavailable(err) ||
						fcm.IsInternal(err) ||
						fcm.IsUnknown(err) {
						// Transient errors. Stop sending this batch.
						log.Println("fcm transient failure", err)
						return
					}

					if fcm.IsMismatchedCredential(err) || fcm.IsInvalidArgument(err) {
						// Config errors
						log.Println("fcm push failed", err)
						return
					}

					if fcm.IsRegistrationTokenNotRegistered(err) {
						// Token is no longer valid.
						store.Devices.Delete(uid, d.DeviceId)
						log.Println("fcm invalid token", err)
					} else {
						log.Println("fcm push", err)
					}
				}
			}
		}
	}
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
