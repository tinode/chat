package vc

import (
	"encoding/json"
	"time"
)

type VideoConferencingInterface interface {
	// Open initializes the video conferencing adapter.
	//
	//	  jsonconf - configuration string
	Open(jsonconf json.RawMessage) error
	// Returns true if video conferencing capabilities are supported.
	IsAvailable() bool
	// Returns video conferencing endpoint url.
	EndpointUrl() string
	// Returns a token to join a VC call in progress for uid in the given topic.
	GetToken(topic, uid string, createdAt time.Time) (string, error)
}

var VideoConferencing VideoConferencingInterface

type vcObj struct{}

func init() {
	VideoConferencing = vcObj{}
}
