package vc

import (
	"encoding/json"
)

type VideoConferencingInterface interface {
	// Open initializes the video conferencing adapter.
	//
	//	  jsonconf - configuration string
	Open(jsonconf json.RawMessage) error
	// Returns true if video conferencing capabilities are supported.
	IsAvailable() bool
	// Returns a token to join a VC call in progress for uid in the given topic.
	GetToken(topic, uid string) (string, error)
}

var VideoConferencing VideoConferencingInterface

type vcObj struct{}

func init() {
	VideoConferencing = vcObj{}
}
