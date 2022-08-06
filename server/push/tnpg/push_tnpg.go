// Package tnpg implements push notification plugin for Tinode Push Gateway.
package tnpg

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/push/common"
	"github.com/tinode/chat/server/push/fcm"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	fcmv1 "google.golang.org/api/fcm/v1"
)

const (
	baseTargetAddress = "https://pushgw.tinode.co/"
	pushPath          = "pushv1"
	subsPath          = "sub"
	pushBatchSize     = 100
	subBatchSize      = 1000
	bufferSize        = 1024
)

var handler Handler

// Handler represents state of TNPG push client.
type Handler struct {
	input   chan *push.Receipt
	channel chan *push.ChannelReq
	stop    chan bool
	pushUrl string
	subUrl  string
}

type configType struct {
	Enabled         bool   `json:"enabled"`
	OrgID           string `json:"org"`
	AuthToken       string `json:"token"`
	DebugPushGWHost string `json:"debug_server"`
}

// subUnsubReq is a request to subscribe/unsubscribe device ID(s) to channel(s) (FCM topic).
// One device to multiple channels or multiple devices to one channel.
type subUnsubReq struct {
	Channel  string   `json:"channel,omitempty"`
	Channels []string `json:"channels,omitempty"`
	Device   string   `json:"device,omitempty"`
	Devices  []string `json:"devices,omitempty"`
	Unsub    bool     `json:"unsub"`
}

type tnpgResponse struct {
	// Push message response only.
	MessageID string `json:"msg_id,omitempty"`
	// Server response HTTP code.
	Code int `json:"code,omitempty"`
	// FCM response code. Both push and sub/unsub response.
	ErrorCode     string `json:"errcode,omitempty"`
	ExtendedError string `json:"exerr,omitempty"`
	ErrorMessage  string `json:"errmsg,omitempty"`
	// Channel sub/unsub response only.
	Index int `json:"index,omitempty"`
}

type batchResponse struct {
	// Number of successfully sent messages.
	SuccessCount int `json:"sent_count"`
	// Number of failures.
	FailureCount int `json:"fail_count"`
	// Error code and message if the entire batch failed.
	FatalCode    string `json:"errcode,omitempty"`
	FatalMessage string `json:"errmsg,omitempty"`
	// Individual reponses in the same order as messages. Could be nil if the entire batch failed.
	Responses []*tnpgResponse `json:"resp,omitempty"`

	// Local values
	httpCode   int
	httpStatus string
}

// Error codes copied from https://github.com/firebase/firebase-admin-go/blob/master/messaging/messaging.go
const (
	internalError                  = "internal-error"
	invalidAPNSCredentials         = "invalid-apns-credentials"
	invalidArgument                = "invalid-argument"
	messageRateExceeded            = "message-rate-exceeded"
	mismatchedCredential           = "mismatched-credential"
	registrationTokenNotRegistered = "registration-token-not-registered"
	serverUnavailable              = "server-unavailable"
	tooManyTopics                  = "too-many-topics"
	unknownError                   = "unknown-error"
)

// Init initializes the handler
func (Handler) Init(jsonconf json.RawMessage) (bool, error) {
	var config configType
	if err := json.Unmarshal(jsonconf, &config); err != nil {
		return false, errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return false, nil
	}

	config.OrgID = strings.TrimSpace(config.OrgID)
	if config.OrgID == "" {
		return false, errors.New("organization name is missing")
	}

	// Convert to lower case to avoid confusion.
	config.OrgID = strings.ToLower(config.OrgID)

	// Construct server URLs.
	serverAddr := baseTargetAddress
	if config.DebugPushGWHost != "" {
		serverAddr = config.DebugPushGWHost
	}
	serverUrl, err := url.Parse(serverAddr)
	if err != nil {
		return false, err
	}
	serverUrl.Path += pushPath + "/" + config.OrgID
	handler.pushUrl = serverUrl.String()
	serverUrl, _ = url.Parse(serverAddr)
	serverUrl.Path += subsPath + "/" + config.OrgID
	handler.subUrl = serverUrl.String()

	handler.input = make(chan *push.Receipt, bufferSize)
	handler.channel = make(chan *push.ChannelReq, bufferSize)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendPushes(rcpt, &config)
			case sub := <-handler.channel:
				go processSubscription(sub, &config)
			case <-handler.stop:
				return
			}
		}
	}()

	return true, nil
}

func postMessage(endpoint string, body interface{}, config *configType) (*batchResponse, error) {
	buf := new(bytes.Buffer)
	gzw := gzip.NewWriter(buf)
	err := json.NewEncoder(gzw).Encode(body)
	gzw.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+config.AuthToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("Content-Encoding", "gzip")
	req.Header.Add("Accept-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	var batch batchResponse
	var reader io.ReadCloser
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		reader, err = gzip.NewReader(resp.Body)
		if err == nil {
			defer reader.Close()
		}
	} else {
		reader = resp.Body
	}

	if err == nil {
		err = json.NewDecoder(reader).Decode(&batch)
	}
	resp.Body.Close()

	if err != nil {
		// Just log the error, but don't report it to caller. The push succeeded.
		logs.Warn.Println("tnpg failed to decode respons:", err)
	}

	batch.httpCode = resp.StatusCode
	batch.httpStatus = resp.Status

	return &batch, nil
}

func sendPushes(rcpt *push.Receipt, config *configType) {
	messages, uids := fcm.PrepareV1Notifications(rcpt, nil)

	n := len(messages)
	for i := 0; i < n; i += pushBatchSize {
		upper := i + pushBatchSize
		if upper > n {
			upper = n
		}
		var payloads []interface{}
		for j := i; j < upper; j++ {
			payloads = append(payloads, messages[j])
		}
		resp, err := postMessage(handler.pushUrl, payloads, config)
		if err != nil {
			logs.Warn.Println("tnpg push request failed:", err)
			break
		}
		if resp.httpCode >= 300 {
			logs.Warn.Println("tnpg push rejected:", resp.httpStatus)
			break
		}
		if resp.FatalCode != "" {
			logs.Err.Println("tnpg push failed:", resp.FatalMessage)
			break
		}
		// Check for expired tokens and other errors.
		handlePushResponse(resp, messages[i:upper], uids[i:upper])
	}
}

func processSubscription(req *push.ChannelReq, config *configType) {
	su := subUnsubReq{
		Unsub: req.Unsub,
	}

	if req.Channel != "" {
		su.Devices = fcm.DevicesForUser(req.Uid)
		su.Channel = req.Channel
	} else if req.DeviceID != "" {
		su.Channels = fcm.ChannelsForUser(req.Uid)
		su.Device = req.DeviceID
	}

	if (len(su.Devices) == 0 && su.Device == "") || (len(su.Channels) == 0 && su.Channel == "") {
		return
	}

	if len(su.Devices) > subBatchSize {
		// It's extremely unlikely for a single user to have this many devices.
		su.Devices = su.Devices[0:subBatchSize]
		logs.Warn.Println("tnpg: user", req.Uid.UserId(), "has more than", subBatchSize, "devices")
	}

	resp, err := postMessage(handler.subUrl, &su, config)
	if err != nil {
		logs.Warn.Println("tnpg channel sub request failed:", err)
		return
	}
	if resp.httpCode >= 300 {
		logs.Warn.Println("tnpg channel sub rejected:", resp.httpStatus)
		return
	}
	if resp.FatalCode != "" {
		logs.Err.Println("tnpg channel sub failed:", resp.FatalMessage)
		return
	}
	// Check for expired tokens and other errors.
	handleSubResponse(resp, req, su.Devices, su.Channels)
}

func handlePushResponse(batch *batchResponse, messages []*fcmv1.Message, uids []types.Uid) {
	if batch.FailureCount <= 0 {
		return
	}

	for i, resp := range batch.Responses {
		switch resp.ErrorCode {
		case "": // no error
		case common.ErrorQuotaExceeded, common.ErrorUnavailable, common.ErrorInternal, common.ErrorUnspecified:
			// Transient errors. Stop sending this batch.
			logs.Warn.Println("tnpg transient failure:", resp.ErrorMessage)
			return
		case common.ErrorInvalidArgument:
			// Usually an invalid token.
			logs.Warn.Println("tnpg invalid argument:", resp.ExtendedError, resp.ErrorMessage)
			if strings.Contains(resp.ExtendedError, "message.token") {
				if err := store.Devices.Delete(uids[i], messages[i].Token); err != nil {
					logs.Warn.Println("tnpg failed to delete invalid token:", err)
				}
			}
		case common.ErrorSenderIDMismatch, common.ErrorThirdPartyAuth:
			// Config errors
			logs.Warn.Println("tnpg invalid config:", resp.ExtendedError, resp.ErrorMessage)
			return
		case common.ErrorUnregistered:
			// Token is no longer valid.
			logs.Info.Println("tnpg invalid token:", resp.ErrorMessage, resp.ExtendedError, resp.MessageID)
			if err := store.Devices.Delete(uids[i], messages[i].Token); err != nil {
				logs.Warn.Println("tnpg failed to delete invalid token:", err)
			}
		default:
			logs.Warn.Println("tnpg unrecognized error:", resp.ErrorCode, resp.ErrorMessage, resp.ExtendedError, resp.Code)
		}
	}
}

func handleSubResponse(batch *batchResponse, req *push.ChannelReq, devices, channels []string) {
	if batch.FailureCount <= 0 {
		return
	}

	var src string
	for _, resp := range batch.Responses {
		if len(devices) > 0 {
			src = devices[resp.Index]
		} else {
			src = channels[resp.Index]
		}
		// FCM documentation sucks. There is no list of possible errors so no action can be taken but logging.
		logs.Warn.Println("fcm sub/unsub error", resp.ErrorCode, req.Uid, src)
	}
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

// Channel returns a channel that the server will use to send group requests to.
// If the adapter blocks, the message will be dropped.
func (Handler) Channel() chan<- *push.ChannelReq {
	return handler.channel
}

// Stop terminates the handler's worker and stops sending pushes.
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("tnpg", &handler)
}
