package tel

import (
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Empty placeholder struct.
type validator struct{}

// Init is a noop.
func (validator) Init(jsonconf string) error {
	return nil
}

// PreCheck validates the credential and parameters without sending an SMS or maing the call.
func (validator) PreCheck(cred string, params interface{}) error {
	// TODO: Check phone format. Format phone for E.164
	// TODO: Check phone uniqueness
	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (validator) Request(user t.Uid, cred, lang string, params interface{}, resp string) error {
	// TODO: actually send a validation SMS or make a call to the provided `cred` here.
	return nil
}

// Find if user exists in the database, and if so return OK. Any response is accepted.
func (validator) Check(user t.Uid, resp string) error {
	// TODO: check response against a database.
	return nil
}

// Delete deletes user's records.
func (validator) Delete(user t.Uid) error {
	return nil
}

func init() {
	store.RegisterValidator("tel", validator{})
}
