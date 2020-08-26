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

	// The number of push messages sent in one batch. FCM constant.
	pushBatchSize = 100

	// The number of sub/unsub requests sent in one batch. FCM constant.
	subBatchSize = 1000

	// Maximum length of a text message in runes. The message is clipped if length is exceeded.
	// TODO: implement intelligent clipping of Drafty messages.
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
	n := len(messages)
	if n == 0 {
		return
	}

	ctx := context.Background()
	for i := 0; i < n; i += pushBatchSize {
		upper := i + pushBatchSize
		if upper > n {
			upper = n
		}
		var batch []*fcm.Message
		for j := i; j < upper; j++ {
			batch = append(batch, messages[j].Message)
		}
		resp, err := handler.client.SendAll(ctx, batch)
		if err != nil {
			// Complete failure.
			log.Println("fcm SendAll failed", err)
			break
		}

		// Check for partial failure.
		if !handlePushErrors(resp, messages[i:upper]) {
			break
		}
	}
}

func processSubscription(req *push.ChannelReq) {
	devices := DevicesForUser(req.Uid)
	if len(devices) == 0 {
		return
	}

	if len(devices) > subBatchSize {
		// It's extremely unlikely for a single user to have this many devices.
		devices = devices[0:subBatchSize]
		log.Println("fcm: user", req.Uid.UserId(), "has more than", subBatchSize, "devices")
	}

	var err error
	var resp *fcm.TopicManagementResponse
	if req.Unsub {
		resp, err = handler.client.UnsubscribeFromTopic(context.Background(), devices, req.Channel)
	} else {
		resp, err = handler.client.SubscribeToTopic(context.Background(), devices, req.Channel)
	}
	if err != nil {
		// Complete failure.
		log.Println("fcm: sub or upsub failed", req.Unsub, err)
	} else {
		// Check for partial failure.
		handleSubErrors(resp, req.Uid, devices)
	}
}

// handlePushError processes errors returned by a call to fcm.SendAll.
// returns false to stop further processing of other messages.
func handlePushErrors(response *fcm.BatchResponse, batch []MessageData) bool {
	if response.FailureCount <= 0 {
		return true
	}

	for i, resp := range response.Responses {
		if !handleFcmError(resp.Error, batch[i].Uid, batch[i].DeviceId) {
			return false
		}
	}
	return true
}

func handleSubErrors(response *fcm.TopicManagementResponse, uid types.Uid, devices []string) {
	if response.FailureCount <= 0 {
		return
	}

	for _, errinfo := range response.Errors {
		// FCM documentation sucks. There is no list of possible errors so no action can be taken but logging.
		log.Println("fcm sub/unsub error", errinfo.Reason, uid, devices[errinfo.Index])
	}
}

func handleFcmError(err error, uid types.Uid, deviceId string) bool {
	if fcm.IsMessageRateExceeded(err) ||
		fcm.IsServerUnavailable(err) ||
		fcm.IsInternal(err) ||
		fcm.IsUnknown(err) {
		// Transient errors. Stop sending this batch.
		log.Println("fcm transient failure", err)
		return false
	}
	if fcm.IsMismatchedCredential(err) || fcm.IsInvalidArgument(err) {
		// Config errors
		log.Println("fcm: request failed", err)
		return false
	}

	if fcm.IsRegistrationTokenNotRegistered(err) {
		// Token is no longer valid.
		log.Println("fcm: invalid token", uid, err)
		if err := store.Devices.Delete(uid, deviceId); err != nil {
			log.Println("fcm: failed to delete invalid token", err)
		}
	} else {
		// All other errors are treated as non-fatal.
		log.Println("fcm error:", err)
	}
	return true
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
