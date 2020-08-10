// Package fcm implements push notification plugin for Google FCM backend.
// Push notifications for Android, iOS and web clients are sent through Google's Firebase Cloud Messaging service.
// Package fcm is push notification plugin using Google FCM.
// https://firebase.google.com/docs/cloud-messaging
package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	fbase "firebase.google.com/go"
	fcm "firebase.google.com/go/messaging"

	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

var handler Handler

const (
	// Size of the input channel buffer.
	bufferSize = 1024

	// Maximum length of a text message in runes
	maxMessageLength = 80
)

// Handler represents the push handler; implements push.PushHandler interface.
type Handler struct {
	input   chan *push.Receipt
	channel chan *push.ChannelReq
	stop    chan bool
	client  *fcm.Client
}

type configType struct {
	Enabled         bool            `json:"enabled"`
	Credentials     json.RawMessage `json:"credentials"`
	CredentialsFile string          `json:"credentials_file"`
	TimeToLive      uint            `json:"time_to_live,omitempty"`
	Android         AndroidConfig   `json:"android,omitempty"`
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

	handler.input = make(chan *push.Receipt, bufferSize)
	handler.channel = make(chan *push.ChannelReq, bufferSize)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendNotifications(rcpt, &config)
			case sub := <-handler.channel:
				go processSubscription(sub)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

func sendNotifications(rcpt *push.Receipt, config *configType) {
	messages := PrepareNotifications(rcpt, &config.Android)
	if len(messages) == 0 {
		return
	}

	ctx := context.Background()
	for _, m := range messages {
		_, err := handler.client.Send(ctx, m.Message)
		if err != nil {
			if handlePushError(err, m.Uid, m.DeviceId) {
				return
			}
		}
	}
}

func processSubscription(req *push.ChannelReq) {
	if len(req.Devices) == 0 {
		return
	}

	ctx := context.Background()

	var err error
	var resp *fcm.TopicManagementResponse
	if req.Unsub {
		resp, err = handler.client.UnsubscribeFromTopic(ctx, req.Devices, req.Channel)
	} else {
		resp, err = handler.client.SubscribeToTopic(ctx, req.Devices, req.Channel)
	}

	if err != nil {
		handlePushError(err)
	} else {

	}
}

// handlePushError processes error returned by a call to FCM.
// returns true to stop further processing of other messages.
func handlePushError(err error, uid types.Uid, deviceId string) bool {
	if fcm.IsMessageRateExceeded(err) ||
		fcm.IsServerUnavailable(err) ||
		fcm.IsInternal(err) ||
		fcm.IsUnknown(err) {
		// Transient errors. Stop sending this batch.
		log.Println("fcm transient failure", err)
		return true
	}

	if fcm.IsMismatchedCredential(err) || fcm.IsInvalidArgument(err) {
		// Config errors
		log.Println("fcm push: failed", err)
		return true
	}

	if fcm.IsRegistrationTokenNotRegistered(err) {
		// Token is no longer valid.
		log.Println("fcm push: invalid token", err)
		err = store.Devices.Delete(uid, deviceId)
		if err != nil {
			log.Println("fcm push: failed to delete invalid token", err)
		}
	} else {
		log.Println("fcm push:", err)
	}
	return false
}

// IsReady checks if the push handler has been initialized.
func (Handler) IsReady() bool {
	return handler.input != nil
}

// Push returns a channel that the server will use to send messages to.
// If the adapter blocks, the message will be dropped.
func (Handler) Push() chan<- *push.Receipt {
	return handler.input
}

// Channel returns a channel for subscribing/unsubscribing devices to FCM topics.
func (Handler) Channel() chan<- *push.ChannelReq {
	return handler.channel
}

// Stop shuts down the handler
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("fcm", &handler)
}
