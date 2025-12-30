// Package fcm implements push notification plugin for Google FCM backend.
// Push notifications for Android, iOS and web clients are sent through Google's Firebase Cloud Messaging service.
// Package fcm is push notification plugin using Google FCM.
// https://firebase.google.com/docs/cloud-messaging
package fcm

import (
	"context"
	"encoding/json"
	"errors"

	fcmv1 "google.golang.org/api/fcm/v1"

	"os"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/pushtype"
	"github.com/tinode/pushtype/iid"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

var handler Handler

const (
	// Size of the input channel buffer.
	bufferSize = 1024

	// The number of sub/unsub requests sent in one batch. FCM constant.
	subBatchSize = 1000
)

// Handler represents the push handler; implements push.PushHandler interface.
type Handler struct {
	input     chan *push.Receipt
	channel   chan *push.ChannelReq
	stop      chan bool
	projectID string
	v1        *fcmv1.Service
	creds     []byte // service account JSON, used for IID calls
}

type configType struct {
	Enabled         bool             `json:"enabled"`
	DryRun          bool             `json:"dry_run"`
	Credentials     json.RawMessage  `json:"credentials"`
	CredentialsFile string           `json:"credentials_file"`
	TimeToLive      int              `json:"time_to_live,omitempty"`
	ApnsBundleID    string           `json:"apns_bundle_id,omitempty"`
	Android         *pushtype.Config `json:"android,omitempty"`
	Apns            *pushtype.Config `json:"apns,omitempty"`
	Webpush         *pushtype.Config `json:"webpush,omitempty"`
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

	if config.Credentials == nil && config.CredentialsFile != "" {
		config.Credentials, err = os.ReadFile(config.CredentialsFile)
		if err != nil {
			return false, err
		}
	}

	if config.Credentials == nil {
		return false, errors.New("missing credentials")
	}

	ctx := context.Background()
	credentials, err := google.CredentialsFromJSON(ctx, config.Credentials, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return false, err
	}
	if credentials.ProjectID == "" {
		return false, errors.New("missing project ID")
	}

	// Store raw credentials JSON for IID calls
	handler.creds = config.Credentials

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
				go sendFcmV1(rcpt, &config)
			case sub := <-handler.channel:
				go processSubscription(sub)
			case <-handler.stop:
				return
			}
		}
	}()

	return true, nil
}

func sendFcmV1(rcpt *push.Receipt, config *configType) {
	messages, uids := PrepareV1Notifications(rcpt, config)
	for i := range messages {
		req := &fcmv1.SendMessageRequest{
			Message:      messages[i],
			ValidateOnly: config.DryRun,
		}
		_, err := handler.v1.Projects.Messages.Send("projects/"+handler.projectID, req).Do()
		if err != nil {
			fcmCode, fcmMsg, decErrs := pushtype.ParseGoogleAPIError(err)
			for _, derr := range decErrs {
				logs.Info.Println("fcm googleapi.Error decoding:", derr)
			}
			switch fcmCode {
			case "": // no error or unknown code
			case pushtype.ErrorQuotaExceeded, pushtype.ErrorUnavailable, pushtype.ErrorInternal, pushtype.ErrorUnspecified:
				// Transient errors. Stop sending this batch.
				logs.Warn.Println("fcm transient failure:", fcmCode, fcmMsg)
				return
			case pushtype.ErrorSenderIDMismatch, pushtype.ErrorInvalidArgument, pushtype.ErrorThirdPartyAuth:
				// Config errors. Stop.
				logs.Warn.Println("fcm invalid config:", fcmCode, fcmMsg)
				return
			case pushtype.ErrorUnregistered:
				// Token is no longer valid. Delete token from DB and continue sending.
				logs.Warn.Println("fcm invalid token:", fcmCode, fcmMsg)
				if err := store.Devices.Delete(uids[i], messages[i].Token); err != nil {
					logs.Warn.Println("tnpg failed to delete invalid token:", err)
				}
			default:
				// Unknown error. Stop sending just in case.
				logs.Warn.Println("tnpg unrecognized error:", fcmCode, fcmMsg)
				return
			}
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
		// No channels or devices to subscribe or unsubscribe.
		return
	}

	if len(devices) > subBatchSize {
		// It's extremely unlikely for a single user to have this many devices.
		devices = devices[0:subBatchSize]
		logs.Warn.Println("fcm: user", req.Uid.UserId(), "has more than", subBatchSize, "devices")
	}

	// Use IID batch endpoints to subscribe/unsubscribe tokens to topics.
	if channel != "" && len(devices) > 0 {
		client, cerr := iid.NewFromCreds(context.Background(), handler.creds)
		if cerr != nil {
			logs.Warn.Println("fcm: sub/unsub failed", req.Unsub, cerr)
			return
		}
		parsed, err := client.BatchManage(channel, devices, !req.Unsub)
		if err != nil {
			logs.Warn.Println("fcm: sub/unsub failed", req.Unsub, err)
			return
		}
		// Inspect per-item results and log failures
		for i, r := range parsed {
			if errStr, ok := r["error"].(string); ok && errStr != "" {
				logs.Warn.Println("fcm sub/unsub error", errStr, req.Uid, devices[i])
			}
		}
		return
	}

	if device != "" && len(channels) > 0 {
		for _, ch := range channels {
			client, cerr := iid.NewFromCreds(context.Background(), handler.creds)
			if cerr != nil {
				logs.Warn.Println("fcm: sub/unsub failed", req.Unsub, cerr)
				return
			}
			parsed, err := client.BatchManage(ch, []string{device}, !req.Unsub)
			if err != nil {
				logs.Warn.Println("fcm: sub/unsub failed", req.Unsub, err)
				return
			}
			for i, r := range parsed {
				if errStr, ok := r["error"].(string); ok && errStr != "" {
					logs.Warn.Println("fcm sub/unsub error", errStr, req.Uid, device)
				}
				_ = i
			}
		}
		return
	}

	// Invalid request: either multiple channels & multiple devices (not supported) or no channels and no devices.
	logs.Err.Println("fcm: user", req.Uid.UserId(), "invalid combination of sub/unsub channels/devices",
		len(devices), len(channels))
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
