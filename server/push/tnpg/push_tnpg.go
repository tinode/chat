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

	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/push/fcm"
)

const targetAddress = "https://pushgw.tinode.co/push"

var handler Handler

type Handler struct {
	input       chan *push.Receipt
	stop        chan bool
}

type configType struct {
	Enabled          bool `json:"enabled"`
	Buffer           int  `json:"buffer"`
	CompressPayloads bool `json:"compress_payloads"`
	AuthToken        string `json:"auth_token"`
	Android          fcm.AndroidConfig `json:"android,omitempty"`
}

// Init initializes the handler
func (Handler) Init(jsonconf string) error {
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return nil
	}

	handler.input = make(chan *push.Receipt, config.Buffer)
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

func postMessage(body []byte, config *configType) (int, string, error) {
	var reader io.Reader
	if config.CompressPayloads {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(body); err != nil {
			return -1, "", err
		}
		gz.Close()
		reader = &buf
	} else {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest("POST", targetAddress, reader)
	if err != nil {
		return -1, "", err
	}
	req.Header.Add("Authorization", config.AuthToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if config.CompressPayloads {
		req.Header.Add("Content-Encoding", "gzip")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, "", err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Status, nil
}

func sendPushes(rcpt *push.Receipt, config *configType) {
	messages := fcm.PrepareNotifications(rcpt, &config.Android)
	if messages == nil {
		return
	}

	var payloads []interface{}
	for _, m := range messages {
		payloads = append(payloads, m.Message)
	}
	msgs, err := json.Marshal(payloads)
	if err != nil {
		log.Println("tnpg push: cannot serialize push messages -", err)
		return
	}
	if code, status, err := postMessage(msgs, config); err != nil {
		log.Println("tnpg push failed:", err)
	} else if code >= 300 {
		log.Println("tnpg push rejected:", status, err)
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
