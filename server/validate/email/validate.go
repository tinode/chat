package email

import (
	"math/rand"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Empty placeholder struct.
type validator struct{}

// Init: initialize validator.
func (validator) Init(jsonconf string) error {
	// Initialize random number generator.
	rand.Seed(time.Now().UnixNano())
	return nil
}

// PreCheck validates the credential and parameters without sending an email.
func (validator) PreCheck(cred string, params interface{}) error {
	_, err := mail.ParseAddress(cred)
	if err != nil {
		return t.ErrMalformed
	}
	// TODO: Check email uniqueness
	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (validator) Request(user t.Uid, cred, lang string, params interface{}, resp string) error {

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

	ts := t.TimeNow()
	return store.Users.SaveCred(&t.Credential{
		CreatedAt: ts,
		UpdatedAt: ts,
		User:      user.String(),
		Method:    email,
		Value:     cred,
		Resp:      resp,
	})
}

// Find if user exists in the database, and if so return OK. Any response is accepted.
func (validator) Confirm(user t.Uid, resp string) error {
	cred, err := store.Users.GetCred(user, "email")
	if err != nil {
		return err
	}

	return store.Users.ConfirmCred(user, "email", resp)
}

// Delete deletes user's records.
func (validator) Delete(user t.Uid) error {
	return nil
}

func init() {
	store.RegisterValidator("email", validator{})
}
