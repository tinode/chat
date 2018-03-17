package email

import (
	"encoding/json"
	"math/rand"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Validator configuration.
type validator struct {
	TemplateFile  string `json:"template"`
	SendFrom      string `json:"sender"`
	DebugResponse string `json:"debug_response"`
	MaxRetries    int    `json:"max_retries"`
}

const (
	maxRetries = 4
)

// Init: initialize validator.
func (v *validator) Init(jsonconf string) error {
	if err := json.Unmarshal([]byte(jsonconf), v); err != nil {
		return err
	}

	// Initialize random number generator.
	rand.Seed(time.Now().UnixNano())

	if v.MaxRetries == 0 {
		v.MaxRetries = maxRetries
	}
	return nil
}

// PreCheck validates the credential and parameters without sending an email.
func (v *validator) PreCheck(cred string, params interface{}) error {
	_, err := mail.ParseAddress(cred)
	if err != nil {
		return t.ErrMalformed
	}
	// TODO: Check email uniqueness
	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (v *validator) Request(user t.Uid, cred, lang string, params interface{}, resp string) error {

	// Email validator cannot accept an immmediate response.
	if resp != "" {
		return t.ErrFailed
	}

	// Generate expected response as a random numeric string between 0 and 999999
	resp = strconv.FormatInt(int64(rand.Intn(1000000)), 10)
	resp = strings.Repeat("0", 6-len(resp)) + resp

	// TODO: send email to the user with `resp`.

	// For instance, see here how to send email from Amazon SES:
	// https://docs.aws.amazon.com/sdk-for-go/api/service/ses/#example_SES_SendEmail_shared00

	// Here are instructions for Google cloud:
	// https://cloud.google.com/appengine/docs/standard/go/mail/sending-receiving-with-mail-api

	return store.Users.SaveCred(&t.Credential{
		User:   user.String(),
		Method: "email",
		Value:  cred,
		Resp:   resp,
	})
}

// Find if user exists in the database, and if so return OK.
func (v *validator) Check(user t.Uid, resp string) error {
	cred, err := store.Users.GetCred(user, "email")
	if err != nil {
		return err
	}

	if cred == nil {
		// Request to validate non-existent credential.
		return t.ErrNotFound
	}

	if cred.Retries > v.MaxRetries {
		return t.ErrPolicy
	}
	if resp == "" {
		return t.ErrFailed
	}

	// Comparing with dummy response too.
	if cred.Resp == resp || v.DebugResponse == resp {
		// Valid response, save confirmation.
		return store.Users.ConfirmCred(user, "email")
	}

	// Invalid response, increment fail counter.
	store.Users.FailCred(user, "email")

	return t.ErrFailed
}

// Delete deletes user's records.
func (v *validator) Delete(user t.Uid) error {
	return nil
}

func init() {
	store.RegisterValidator("email", &validator{})
}
