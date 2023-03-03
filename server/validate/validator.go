// Package validate defines an interface which must be implmented by credential validators.
package validate

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"text/template"

	t "github.com/tinode/chat/server/store/types"
)

// Validator handles validation of user's credentials, like email or phone.
type Validator interface {
	// Init initializes the validator.
	Init(jsonconf string) error

	// IsInitialized returns true if the validator is initialized.
	IsInitialized() bool

	// PreCheck pre-validates the credential without sending an actual request for validation:
	// check uniqueness (if appropriate), format, etc
	// Returns normalized credential prefixed with an appropriate namespace prefix.
	PreCheck(cred string, params map[string]interface{}) (string, error)

	// Request sends a request for confirmation to the user. Returns true if it's a new credential,
	// false if it re-sent request for an existing unconfirmed credential.
	//   user: UID of the user making the request.
	//   cred: credential being validated, such as email or phone.
	//   lang: user's human language as repored in the session.
	//   resp: optional response if user already has it (i.e. captcha/recaptcha).
	//   tmpToken: temporary authentication token to include in the request.
	Request(user t.Uid, cred, lang, resp string, tmpToken []byte) (bool, error)

	// ResetSecret sends a message with instructions for resetting an authentication secret.
	//   cred: address to use for the message.
	//   scheme: authentication scheme being reset.
	//   lang: human language as reported in the session.
	//   tmpToken: temporary authentication token
	//   params: authentication params.
	ResetSecret(cred, scheme, lang string, tmpToken []byte, params map[string]interface{}) error

	// Check checks validity of user's response.
	// Returns the value of validated credential on success.
	Check(user t.Uid, resp string) (string, error)

	// Remove deletes or deactivates user's given value.
	Remove(user t.Uid, value string) error

	// Delete deletes user's record.
	Delete(user t.Uid) error
}

func ValidateHostURL(origUrl string) (string, error) {
	hostUrl, err := url.Parse(origUrl)
	if err != nil {
		return "", err
	}
	if !hostUrl.IsAbs() {
		return "", errors.New("host_url must be absolute")
	}
	if hostUrl.Hostname() == "" {
		return "", errors.New("invalid host_url")
	}
	if hostUrl.Fragment != "" {
		return "", errors.New("fragment is not allowed in host_url")
	}
	if hostUrl.Path == "" {
		hostUrl.Path = "/"
	}
	return hostUrl.String(), nil
}

func ExecuteTemplate(template *template.Template, parts []string, params map[string]interface{}) (map[string]string, error) {
	content := map[string]string{}
	buffer := new(bytes.Buffer)

	if parts == nil {
		if err := template.Execute(buffer, params); err != nil {
			return nil, err
		}
		content[""] = buffer.String()
	} else {
		for _, part := range parts {
			buffer.Reset()
			if templBody := template.Lookup(part); templBody != nil {
				if err := templBody.Execute(buffer, params); err != nil {
					return nil, err
				}
			}
			content[part] = buffer.String()
		}
	}

	return content, nil
}

func ResolveTemplatePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	curwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return filepath.Clean(filepath.Join(curwd, path)), nil
}

func ReadTemplateFile(pathTempl *template.Template, lang string) (*template.Template, string, error) {
	buffer := bytes.Buffer{}
	err := pathTempl.Execute(&buffer, map[string]interface{}{"Language": lang})
	path := buffer.String()
	if err != nil {
		return nil, path, fmt.Errorf("reading %s: %w", path, err)
	}

	templ, err := template.ParseFiles(path)
	return templ, path, err
}
