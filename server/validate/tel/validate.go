// Package tel is an incomplete implementation of SMS or voice credential validator.
package tel

import (
	"github.com/nyaruka/phonenumbers"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

const validatorName = "tel"

// Empty placeholder struct.
type validator struct {
	DebugResponse string `json:"debug_response"`
	MaxRetries    int    `json:"max_retries"`
}

// Init is a noop.
func (v *validator) Init(jsonconf string) error {
	// Implement: Parse config and initialize SMS service.

	v.MaxRetries = 1000
	v.DebugResponse = "123456"

	return nil
}

// PreCheck validates the credential and parameters without sending an SMS or making the call.
// If credential is valid it's formatted and prefixed with a tag namespace.
func (*validator) PreCheck(cred string, params map[string]interface{}) (string, error) {
	countryCode, ok := params["countryCode"].(string)
	if !ok {
		countryCode = "US"
	}
	if num, err := phonenumbers.Parse(cred, countryCode); err == nil {
		// It's a phone number. Not checking the length because phone numbers cannot be that long.
		return validatorName + ":" + phonenumbers.Format(num, phonenumbers.E164), nil
	}
	return "", t.ErrMalformed
}

// Request sends a request for confirmation to the user: makes a record in DB  and nothing else.
func (*validator) Request(user t.Uid, cred, lang, resp string, tmpToken []byte) (bool, error) {
	// TODO: actually send a validation SMS or make a call to the provided `cred` here.
	return true, nil
}

// ResetSecret sends a message with instructions for resetting an authentication secret.
func (*validator) ResetSecret(cred, scheme, lang string, tmpToken []byte, params map[string]interface{}) error {
	// TODO: send SMS with rest instructions.
	return nil
}

// Check checks validity of user's response.
func (v *validator) Check(user t.Uid, resp string) (string, error) {
	cred, err := store.Users.GetActiveCred(user, validatorName)
	if err != nil {
		return "", err
	}

	if cred == nil {
		// Request to validate non-existent credential.
		return "", t.ErrNotFound
	}

	if cred.Retries > v.MaxRetries {
		return "", t.ErrPolicy
	}

	if resp == "" {
		return "", t.ErrCredentials
	}

	// Comparing with dummy response too.
	if cred.Resp == resp || v.DebugResponse == resp {
		// Valid response, save confirmation.
		return cred.Value, store.Users.ConfirmCred(user, validatorName)
	}

	// Invalid response, increment fail counter, ignore possible error.
	store.Users.FailCred(user, validatorName)

	return "", t.ErrCredentials
}

// Delete deletes user's records. Returns deleted credentials.
func (*validator) Delete(user t.Uid) error {
	return store.Users.DelCred(user, validatorName, "")
}

// Remove or disable the given record
func (*validator) Remove(user t.Uid, value string) error {
	return store.Users.DelCred(user, validatorName, value)
}

// Implement sending a text message
func (*validator) send(to, body string) error {
	return nil
}

func init() {
	store.RegisterValidator(validatorName, &validator{})
}
