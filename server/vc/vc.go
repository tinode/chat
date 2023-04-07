//go:build vc
// +build vc

package vc

import (
	"encoding/json"

	adapter "github.com/tinode/videoconferencing"
)

var adp *adapter.VideoConferencing

func (vcObj) Open(jsonconf json.RawMessage) error {
	adp = &adapter.VideoConferencing{}
	return adp.Open(jsonconf)
}

func (vcObj) IsAvailable() bool {
	return adp != nil && adp.IsReady()
}

func (vcObj) GetToken(topic, uid string) (string, error) {
	return adp.GetToken(topic, uid)
}
