// Package fcm implements push notification plugin for Google FCM backend.
// Push notifications for Android, iOS and web clients are sent through Google's Firebase Cloud Messaging service.
// Package fcm is push notification plugin using Google FCM.
// https://firebase.google.com/docs/cloud-messaging
package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	fbase "firebase.google.com/go"
	legacy "firebase.google.com/go/messaging"
	fcmv1 "google.golang.org/api/fcm/v1"

	"github.com/tinode/chat/server/logs"
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
)

// Handler represents the push handler; implements push.PushHandler interface.
type Handler struct {
	input     chan *push.Receipt
	channel   chan *push.ChannelReq
	stop      chan bool
	projectID string

	client *legacy.Client
	v1     *fcmv1.Service
}

type configType struct {
	Enabled         bool            `json:"enabled"`
	DryRun          bool            `json:"dry_run"`
	Credentials     json.RawMessage `json:"credentials"`
	CredentialsFile string          `json:"credentials_file"`
	TimeToLive      uint            `json:"time_to_live,omitempty"`
	Android         *AndroidConfig  `json:"android,omitempty"`
	Apns            *ApnsConfig     `json:"apns,omitempty"`
	Webpush         *WebpushConfig  `json:"webpush,omitempty"`
}

// Init initializes the push handler
func (Handler) Init(jsonconf json.RawMessage) (bool, error) {

	var config configType
	err := json.Unmarshal([]byte(jsonconf), &config)
	if err != nil {
		return false, errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return false, nil
	}

	ctx := context.Background()

	if config.Credentials == nil && config.CredentialsFile != "" {
		config.Credentials, err = os.ReadFile(config.CredentialsFile)
		if err != nil {
			return false, err
		}
	}

	if config.Credentials == nil {
		return false, errors.New("missing credentials")
	}

	credentials, err := google.CredentialsFromJSON(ctx, config.Credentials, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return false, err
	}
	if credentials.ProjectID == "" {
		return false, errors.New("missing project ID")
	}

	app, err := fbase.NewApp(ctx, &fbase.Config{}, option.WithCredentials(credentials))
	if err != nil {
		return false, err
	}

	handler.client, err = app.Messaging(ctx)
	if err != nil {
		return false, err
	}

	handler.v1, err = fcmv1.NewService(ctx, option.WithCredentials(credentials), option.WithScopes(fcmv1.FirebaseMessagingScope))
	if err != nil {
		return false, err
	}

	handler.input = make(chan *push.Receipt, bufferSize)
	handler.channel = make(chan *push.ChannelReq, bufferSize)
	handler.stop = make(chan bool, 1)
	handler.projectID = credentials.ProjectID

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendLegacy(rcpt, &config)
			case sub := <-handler.channel:
				go processSubscription(sub)
			case <-handler.stop:
				return
			}
		}
	}()

	return true, nil
}

func sendLegacy(rcpt *push.Receipt, config *configType) {
	messages := PrepareLegacyNotifications(rcpt, &config.Android)
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
		var batch []*legacy.Message
		for j := i; j < upper; j++ {
			batch = append(batch, messages[j].Message)
		}
		resp, err := handler.client.SendAll(ctx, batch)
		if err != nil {
			// Complete failure.
			logs.Warn.Println("fcm SendAll failed", err)
			break
		}

		// Check for partial failure.
		if !handlePushErrors(resp, messages[i:upper]) {
			break
		}
	}
}

func sendFcmV1(rcpt *push.Receipt, config *configType) {
	messages := PrepareV1Notifications(rcpt, config)
	for i := range messages {
		req := &fcmv1.SendMessageRequest{
			Message:      messages[i],
			ValidateOnly: config.DryRun,
		}
		_, err := handler.v1.Projects.Messages.Send("projects/"+handler.projectID, req).Do()
		if err != nil {
			logs.Err.Println("fcm push failed:", err)
			break
		}
	}
}

func processSubscription(req *push.ChannelReq) {
	var channel string
	var devices []string
	var device string
	var channels []string

	if req.Channel != "" {
		devices = DevicesForUser(req.Uid)
		channel = req.Channel
	} else if req.DeviceID != "" {
		channels = ChannelsForUser(req.Uid)
		device = req.DeviceID
	}

	if (len(devices) == 0 && device == "") || (len(channels) == 0 && channel == "") {
		// No channels or devces to subscribe or unsubscribe.
		return
	}

	if len(devices) > subBatchSize {
		// It's extremely unlikely for a single user to have this many devices.
		devices = devices[0:subBatchSize]
		logs.Warn.Println("fcm: user", req.Uid.UserId(), "has more than", subBatchSize, "devices")
	}

	var err error
	var resp *legacy.TopicManagementResponse
	if channel != "" && len(devices) > 0 {
		if req.Unsub {
			resp, err = handler.client.UnsubscribeFromTopic(context.Background(), devices, channel)
		} else {
			resp, err = handler.client.SubscribeToTopic(context.Background(), devices, channel)
		}
		if err != nil {
			// Complete failure.
			logs.Warn.Println("fcm: sub or upsub failed", req.Unsub, err)
		} else {
			// Check for partial failure.
			handleSubErrors(resp, req.Uid, devices)
		}
		return
	}

	if device != "" && len(channels) > 0 {
		devices := []string{device}
		for _, channel := range channels {
			if req.Unsub {
				resp, err = handler.client.UnsubscribeFromTopic(context.Background(), devices, channel)
			} else {
				resp, err = handler.client.SubscribeToTopic(context.Background(), devices, channel)
			}
			if err != nil {
				// Complete failure.
				logs.Warn.Println("fcm: sub or upsub failed", req.Unsub, err)
				break
			}
			// Check for partial failure.
			handleSubErrors(resp, req.Uid, devices)
		}
		return
	}

	// Invalid request: either multiple channels & multiple devices (not supported) or no channels and no devices.
	logs.Err.Println("fcm: user", req.Uid.UserId(), "invalid combination of sub/unsub channels/devices",
		len(devices), len(channels))
}

// handlePushError processes errors returned by a call to fcm.SendAll.
// returns false to stop further processing of other messages.
func handlePushErrors(response *legacy.BatchResponse, batch []MessageData) bool {
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

func handleSubErrors(response *legacy.TopicManagementResponse, uid types.Uid, devices []string) {
	if response.FailureCount <= 0 {
		return
	}

	for _, errinfo := range response.Errors {
		// FCM documentation sucks. There is no list of possible errors so no action can be taken but logging.
		logs.Warn.Println("fcm sub/unsub error", errinfo.Reason, uid, devices[errinfo.Index])
	}
}

func handleFcmError(err error, uid types.Uid, deviceId string) bool {
	if legacy.IsMessageRateExceeded(err) ||
		legacy.IsServerUnavailable(err) ||
		legacy.IsInternal(err) ||
		legacy.IsUnknown(err) {
		// Transient errors. Stop sending this batch.
		logs.Warn.Println("fcm transient failure", err)
		return false
	}
	if legacy.IsMismatchedCredential(err) || legacy.IsInvalidArgument(err) {
		// Config errors
		logs.Warn.Println("fcm: request failed", err)
		return false
	}

	if legacy.IsRegistrationTokenNotRegistered(err) {
		// Token is no longer valid.
		logs.Warn.Println("fcm: invalid token", uid, err)
		if err := store.Devices.Delete(uid, deviceId); err != nil {
			logs.Warn.Println("fcm: failed to delete invalid token", err)
		}
	} else {
		// All other errors are treated as non-fatal.
		logs.Warn.Println("fcm error:", err)
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
