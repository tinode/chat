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
	// Maximum call duration.
	CallMaxDuration() time.Duration
	// Returns a token to join a VC call in progress for uid in the given topic.
	GetToken(topic, uid string, canPublish bool, createdAt time.Time) (string, error)
	// Terminates a video conference in the given topic.
	TerminateCall(topic string) error
}

var VideoConferencing VideoConferencingInterface

type vcObj struct{}

func init() {
	VideoConferencing = vcObj{}
}
