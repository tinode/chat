//go:build !vc
// +build !vc

package vc

import (
	"encoding/json"
	"errors"
)

func (vcObj) Open(jsonconf json.RawMessage) error {
	return errors.New("Video conferencing not available")
}

func (vcObj) IsAvailable() bool {
	return false
}

func (vcObj) GetToken(topic, uid string) (string, error) {
	return "", errors.New("Video conferencing not available")
}
