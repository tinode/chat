package email

import (
	"net/mail"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Empty placeholder struct.
type validator struct{}

// Init is a noop.
func (validator) Init(jsonconf string) error {
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

// Send a request for confirmation to the user: makes a record in DB  and nothing else. May immediately provide
// a response.
func (validator) Request(user t.Uid, cred, lang string, params interface{}, resp string) error {
	// TODO: send email to the user.
	return store.Users.SaveCred(user, "email", cred, params)
}

// Find if user exists in the database, and if so return OK. Any response is accepted.
func (validator) Confirm(user t.Uid, resp string) error {
	// TODO: check response against a database.
	return store.Users.ConfirmCred(user, "email", resp)
}

// Delete deletes user's records.
func (validator) Delete(user t.Uid) error {
	return nil
}

func init() {
	store.RegisterValidator("email", validator{})
}
