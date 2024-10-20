package tel

import (
	"encoding/json"

	"github.com/twilio/twilio-go"
	twapi "github.com/twilio/twilio-go/rest/api/v2010"
)

type twilioConfig struct {
	AccountSid string `json:"account_sid"`
	AuthToken  string `json:"auth_token"`
}

var twilioClient *twilio.RestClient

func twilioInit(jsonconf json.RawMessage) error {
	var conf twilioConfig

	if err := json.Unmarshal(jsonconf, &conf); err != nil {
		return err
	}

	twilioClient = twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: conf.AccountSid,
		Password: conf.AuthToken,
	})

	return nil
}

func twilioSend(from, to, body string) error {
	_, err := twilioClient.Api.CreateMessage(&twapi.CreateMessageParams{
		From: &from,
		To:   &to,
		Body: &body,
	})
	return err
}
