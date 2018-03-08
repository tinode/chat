// Package validate defines an interface which must be implmented by credential validators.
package validate

import (
	t "github.com/tinode/chat/server/store/types"
)

// Validator handles validation of user's credentials, like email or phone.
type Validator interface {
	// Initialize validator
	Init(jsonconf string) error

	// Send a request for confirmation to the user.
	// 	user: UID of the user making the request.
	// 	cred: credential being validated, such as email or phone.
	//  lang: user's human language as repored in the session.
	// 	params: optional parameters dependent on the validation method, such as use of SMS or voice call
	Request(user t.Uid, cred, lang string, params map[string]interface{}) error

	// Check user response.
	Confirm(resp string) (t.Uid, error)
}
