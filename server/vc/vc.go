//go:build vc
// +build vc

package vc

import (
	"encoding/json"
	"time"

	adapter "github.com/tinode/chat-vc/videoconferencing"
)

var adp *adapter.VideoConferencing

func (vcObj) Open(jsonconf json.RawMessage) error {
	adp = &adapter.VideoConferencing{}
	return adp.Open(jsonconf)
}

func (vcObj) IsAvailable() bool {
	return adp != nil && adp.IsReady()
}

func (vcObj) EndpointUrl() string {
	return adp.EndpointUrl()
}

func (vcObj) CallMaxDuration() time.Duration {
	return adp.CallMaxDuration()
}

func (vcObj) GetToken(topic, uid string, canPublish bool, createdAt time.Time) (string, error) {
	return adp.GetToken(topic, uid, canPublish, createdAt)
}

func (vcObj) TerminateCall(topic string) error {
	return adp.TerminateCall(topic)
}
