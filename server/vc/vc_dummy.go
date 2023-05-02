//go:build !vc
// +build !vc

package vc

import (
	"encoding/json"
	"errors"
	"time"
)

func (vcObj) Open(jsonconf json.RawMessage) error {
	return errors.New("Video conferencing not available")
}

func (vcObj) IsAvailable() bool {
	return false
}

func (vcObj) EndpointUrl() string {
	return ""
}

func (vcObj) CallMaxDuration() time.Duration {
	// Some random constant.
	return 10 * time.Minute
}

func (vcObj) GetToken(topic, uid string, createdAt time.Time) (string, error) {
	return "", errors.New("Video conferencing not available")
}

func (vcObj) TerminateCall(topic string) error {
	return errors.New("Video conferencing not implemented")
}
