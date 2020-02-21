// Package email is a credential validator which uses an external SMTP server.
package email

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	ht "html/template"
	"log"
	"math/rand"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Validator configuration.
type validator struct {
	// Base URL of the web client.
	HostUrl string `json:"host_url"`
	// List of languages supported by templates
	Languages []string `json:"languages"`
	// Path to email validation templates, either a template itself or a literal string.
	ValidationTemplFile string `json:"validation_templ"`
	// Path to password rest templates.
	ResetTemplFile string `json:"reset_templ"`
	// Sender email.
	SendFrom string `json:"sender"`
	// Login to use for SMTP authentication.
	Login string `json:"login"`
	// Password to use for SMTP authentication.
	SenderPassword string `json:"sender_password"`
	// Optional response which bypasses the validation.
	DebugResponse string `json:"debug_response"`
	// Number of validation attempts before email is locked.
	MaxRetries int `json:"max_retries"`
	// Address of the SMTP server.
	SMTPAddr string `json:"smtp_server"`
	// Posrt of the SMTP server.
	SMTPPort string `json:"smtp_port"`
	// Optional whitelist of email domains accepted for registration.
	Domains []string `json:"domains"`

	validationTempl map[string]*ht.Template
	resetTempl      map[string]*ht.Template
	auth            smtp.Auth
	senderEmail     string
}

const (
	validatorName = "email"

	maxRetries  = 4
	defaultPort = "25"

	// Technically email could be up to 255 bytes long but practically 128 is enough.
	maxEmailLength = 128

	// codeLength = log10(maxCodeValue)
	codeLength   = 6
	maxCodeValue = 1000000
)

func resolveTemplatePath(path string) string {
	// If a relative path is provided, try to resolve it relative to the exec file location,
	// not whatever directory the user is in.
	if !filepath.IsAbs(path) {
		basepath, err := os.Executable()
		if err == nil {
			path = filepath.Join(filepath.Dir(basepath), path)
		}
	}
	return path
}

func findTempleFile(path *ht.Template, lang string) (*ht.Template, error) {
	var path bytes.Buffer
	err = validationPathTempl.Execute(path, map[string]interface{}{"Language": lang})
	if err != nil {
		return nil, err
	}
	return ht.ParseFiles(string(path.Bytes()))
}

// Init: initialize validator.
func (v *validator) Init(jsonconf string) error {
	var err error
	if err = json.Unmarshal([]byte(jsonconf), v); err != nil {
		return err
	}

	sender, err := mail.ParseAddress(v.SendFrom)
	if err != nil {
		return err
	}
	v.senderEmail = sender.Address

	// Check if login is provided explicitly. Otherwise parse Sender and use that as login for authentication.
	if v.Login != "" {
		v.auth = smtp.PlainAuth("", v.Login, v.SenderPassword, v.SMTPAddr)
	} else {
		// SendFrom could be an RFC 5322 address of the form "John Doe <jdoe@example.com>". Parse it.
		v.auth = smtp.PlainAuth("", v.senderEmail, v.SenderPassword, v.SMTPAddr)
	}

	// Resolve optionally internationalized email templates.

	// If user has provided no languages, use "-" as default language.
	if len(v.Languages) == 0 {
		v.Languages = append(v.Languages, "-")
	}

	// Optionally resolve paths against the location of this executable file.
	v.ValidationTemplFile = resolveTemplatePath(v.ValidationTemplFile)
	v.ResetTemplFile = resolveTemplatePath(v.ValidationTemplFile)

	// Paths to templates could be templates themselves: they may be language-dependent.
	var validationPathTempl, resetPathTempl *ht.Template
	validationPathTempl, err = ht.New("validation").Parse(v.ValidationTemplFile)
	if err != nil {
		return err
	}
	resetPathTempl, err = ht.New("reset").Parse(v.ResetTemplFile)
	if err != nil {
		return err
	}

	// Find actual content templates for each defined language.
	for _, lang := range v.Languages {
		v.validationTempl[lang], err = findTempleFile(validationPathTempl, lang)
		if err != nil {
			return err
		}

		v.resetTempl[lang], err = findTempleFile(resetPathTempl, lang)
		if err != nil {
			return err
		}
	}

	// Initialize random number generator.
	rand.Seed(time.Now().UnixNano())

	hostUrl, err := url.Parse(v.HostUrl)
	if err != nil {
		return err
	}
	if !hostUrl.IsAbs() {
		return errors.New("host_url must be absolute")
	}
	if hostUrl.Hostname() == "" {
		return errors.New("invalid host_url")
	}
	if hostUrl.Fragment != "" {
		return errors.New("fragment is not allowed in host_url")
	}
	if hostUrl.Path == "" {
		hostUrl.Path = "/"
	}
	v.HostUrl = hostUrl.String()

	if v.MaxRetries == 0 {
		v.MaxRetries = maxRetries
	}
	if v.SMTPPort == "" {
		v.SMTPPort = defaultPort
	}

	return nil
}

// PreCheck validates the credential and parameters without sending an email.
func (v *validator) PreCheck(cred string, params interface{}) error {
	if len(cred) > maxEmailLength {
		return t.ErrMalformed
	}

	// The email must be plain user@domain.
	addr, err := mail.ParseAddress(cred)
	if err != nil || addr.Address != cred {
		return t.ErrMalformed
	}

	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	addr.Address = strings.ToLower(addr.Address)

	// If a whitelist of domains is provided, make sure the email belongs to the list.
	if len(v.Domains) > 0 {
		// Parse email into user and domain parts.
		parts := strings.Split(addr.Address, "@")
		if len(parts) != 2 {
			return t.ErrMalformed
		}

		var found bool
		for _, domain := range v.Domains {
			if domain == parts[1] {
				found = true
				break
			}
		}

		if !found {
			return t.ErrPolicy
		}
	}

	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (v *validator) Request(user t.Uid, email, lang, resp string, tmpToken []byte) (bool, error) {
	// Email validator cannot accept an immediate response.
	if resp != "" {
		return false, t.ErrFailed
	}

	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	email = strings.ToLower(email)

	token := make([]byte, base64.URLEncoding.EncodedLen(len(tmpToken)))
	base64.URLEncoding.Encode(token, tmpToken)

	// Generate expected response as a random numeric string between 0 and 999999
	resp = strconv.FormatInt(int64(rand.Intn(maxCodeValue)), 10)
	resp = strings.Repeat("0", codeLength-len(resp)) + resp

	body := new(bytes.Buffer)
	if err := v.htmlValidationTempl.Execute(body, map[string]interface{}{
		"Token":   string(token),
		"Code":    resp,
		"HostUrl": v.HostUrl}); err != nil {
		return false, err
	}

	// Create or update validation record in DB.
	isNew, err := store.Users.UpsertCred(&t.Credential{
		User:   user.String(),
		Method: validatorName,
		Value:  email,
		Resp:   resp})
	if err != nil {
		return false, err
	}

	// Send email without blocking. Email sending may take long time.
	go v.send(email, v.ValidationSubject, string(body.Bytes()))

	return isNew, nil
}

// ResetSecret sends a message with instructions for resetting an authentication secret.
func (v *validator) ResetSecret(email, scheme, lang string, tmpToken []byte) error {
	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	email = strings.ToLower(email)

	token := make([]byte, base64.URLEncoding.EncodedLen(len(tmpToken)))
	base64.URLEncoding.Encode(token, tmpToken)
	body := new(bytes.Buffer)
	if err := v.htmlResetTempl.Execute(body, map[string]interface{}{
		"Token":   string(token),
		"Scheme":  scheme,
		"HostUrl": v.HostUrl}); err != nil {
		return err
	}

	// Send email without blocking. Email sending may take long time.
	go v.send(email, v.ResetSubject, string(body.Bytes()))

	return nil
}

// Check checks if the provided validation response matches the expected response.
// Returns the value of validated credential on success.
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

// Delete deletes user's records.
func (v *validator) Delete(user t.Uid) error {
	return store.Users.DelCred(user, validatorName, "")
}

// Remove deactivates or removes user's credential.
func (v *validator) Remove(user t.Uid, value string) error {
	return store.Users.DelCred(user, validatorName, value)
}

// This is a basic SMTP sender which connects to a server using login/password.
// -
// See here how to send email from Amazon SES:
// https://docs.aws.amazon.com/sdk-for-go/api/service/ses/#example_SES_SendEmail_shared00
// -
// Here are instructions for Google cloud:
// https://cloud.google.com/appengine/docs/standard/go/mail/sending-receiving-with-mail-api
func (v *validator) send(to, subj, body string) error {
	err := smtp.SendMail(v.SMTPAddr+":"+v.SMTPPort, v.auth, v.senderEmail, []string{to},
		[]byte("From: "+v.SendFrom+
			"\nTo: "+to+
			"\nSubject: "+subj+
			"\nMIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"+
			body))

	if err != nil {
		log.Println("SMTP error", to, err)
	}

	return err
}

func init() {
	store.RegisterValidator(validatorName, &validator{})
}
