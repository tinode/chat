// Package tnpg implements push notification plugin for Tinode Push Gateway.
package tnpg

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/push/fcm"
	"github.com/tinode/chat/server/store"
)

const (
	baseTargetAddress = "https://pushgw.tinode.co/push/"
	batchSize         = 100
	bufferSize        = 1024
)

var handler Handler

// Handler represents state of TNPG push client.
type Handler struct {
	input   chan *push.Receipt
	stop    chan bool
	postUrl string
}

type configType struct {
	Enabled   bool   `json:"enabled"`
	OrgName   string `json:"org"`
	AuthToken string `json:"token"`
}

type tnpgResponse struct {
	MessageID    string `json:"msg_id,omitempty"`
	ErrorCode    string `json:"errcode,omitempty"`
	ErrorMessage string `json:"errmsg,omitempty"`
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
func (Handler) Init(jsonconf string) error {
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return nil
	}

	if config.OrgName == "" {
		return errors.New("push.tnpg.org not specified.")
	}

	handler.postUrl = baseTargetAddress + config.OrgName
	handler.input = make(chan *push.Receipt, bufferSize)
	handler.stop = make(chan bool, 1)

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go sendPushes(rcpt, &config)
			case <-handler.stop:
				return
			}
		}
	}()

	return nil
}

func postMessage(body interface{}, config *configType) (*batchResponse, error) {
	buf := new(bytes.Buffer)
	gzw := gzip.NewWriter(buf)
	err := json.NewEncoder(gzw).Encode(body)
	gzw.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", handler.postUrl, buf)
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
		log.Println("tnpg failed to decode response", err)
	}

	batch.httpCode = resp.StatusCode
	batch.httpStatus = resp.Status

	return &batch, nil
}

func sendPushes(rcpt *push.Receipt, config *configType) {
	messages := fcm.PrepareNotifications(rcpt, nil)
	if len(messages) == 0 {
		return
	}

	n := len(messages)
	for i := 0; i < n; i += batchSize {
		upper := i + batchSize
		if upper > n {
			upper = n
		}
		var payloads []interface{}
		for j := i; j < upper; j++ {
			payloads = append(payloads, messages[j].Message)
		}
		if resp, err := postMessage(payloads, config); err != nil {
			log.Println("tnpg push request failed:", err)
			break
		} else if resp.httpCode >= 300 {
			log.Println("tnpg push rejected:", resp.httpStatus)
			break
		} else if resp.FatalCode != "" {
			log.Println("tnpg push failed:", resp.FatalMessage)
			break
		} else {
			// Check for expired tokens and other errors.
			handleResponse(resp, messages[i:upper])
		}
	}
}

func handleResponse(batch *batchResponse, messages []fcm.MessageData) {
	if batch.FailureCount <= 0 {
		return
	}

	for i, resp := range batch.Responses {
		switch resp.ErrorCode {
		case "": // no error
		case messageRateExceeded, serverUnavailable, internalError, unknownError:
			// Transient errors. Stop sending this batch.
			log.Println("tnpg transient failure", resp.ErrorMessage)
			return
		case mismatchedCredential, invalidArgument:
			// Config errors
			log.Println("tnpg invalid config", resp.ErrorMessage)
			return
		case registrationTokenNotRegistered:
			// Token is no longer valid.
			log.Println("tnpg invalid token", resp.ErrorMessage)
			if err := store.Devices.Delete(messages[i].Uid, messages[i].DeviceId); err != nil {
				log.Println("tnpg: failed to delete invalid token", err)
			}
		default:
			log.Println("tnpg returned error", resp.ErrorMessage)
		}
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

// Stop terminates the handler's worker and stops sending pushes.
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("tnpg", &handler)
}
