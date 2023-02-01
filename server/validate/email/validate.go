// Package email is a credential validator which uses an external SMTP server.
package email

import (
	"bytes"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"mime"
	qp "mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	textt "text/template"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	"github.com/tinode/chat/server/validate"
	i18n "golang.org/x/text/language"
)

// Validator configuration.
type validator struct {
	// Base URL of the web client.
	HostUrl string `json:"host_url"`
	// List of languages supported by templates.
	Languages []string `json:"languages"`
	// Path to email validation templates, either a template itself or a literal string.
	ValidationTemplFile string `json:"validation_templ"`
	// Path to templates for resetting the authentication secret.
	ResetTemplFile string `json:"reset_secret_templ"`
	// Sender RFC 5322 email address.
	SendFrom string `json:"sender"`
	// Login to use for SMTP authentication.
	Login string `json:"login"`
	// Password to use for SMTP authentication.
	SenderPassword string `json:"sender_password"`
	// Authentication mechanism to use, optional. One of "login", "md5", "plain" (default).
	AuthMechanism string `json:"auth_mechanism"`
	// Optional response which bypasses the validation.
	DebugResponse string `json:"debug_response"`
	// Number of validation attempts before email is locked.
	MaxRetries int `json:"max_retries"`
	// Address of the SMTP server.
	SMTPAddr string `json:"smtp_server"`
	// Port of the SMTP server.
	SMTPPort string `json:"smtp_port"`
	// ServerName used in SMTP HELO/EHLO command.
	SMTPHeloHost string `json:"smtp_helo_host"`
	// Skip verification of the server's certificate chain and host name.
	// In this mode, TLS is susceptible to machine-in-the-middle attacks.
	TLSInsecureSkipVerify bool `json:"insecure_skip_verify"`
	// Optional whitelist of email domains accepted for registration.
	Domains []string `json:"domains"`
	// Length of secret numeric code to sent for validation.
	CodeLength int `json:"code_length"`

	// Must use index into language array instead of language tags because language.Matcher is brain damaged:
	// https://github.com/golang/go/issues/24211
	validationTempl []*textt.Template
	resetTempl      []*textt.Template
	auth            smtp.Auth
	senderEmail     string
	langMatcher     i18n.Matcher
	maxCodeValue    *big.Int
}

const (
	validatorName = "email"

	defaultMaxRetries = 3
	defaultPort       = "25"

	// Technically email could be up to 255 bytes long but practically 128 is enough.
	maxEmailLength = 128

	// Default code length when one is not provided in the config
	defaultCodeLength = 6
)

// Email template parts
var templateParts = []string{"subject", "body_plain", "body_html"}

// Init: initialize validator.
func (v *validator) Init(jsonconf string) error {
	if err := json.Unmarshal([]byte(jsonconf), v); err != nil {
		return err
	}

	sender, err := mail.ParseAddress(v.SendFrom)
	if err != nil {
		return err
	}
	v.senderEmail = sender.Address

	// Enable auth if login is provided.
	if v.Login != "" {
		mechanism := strings.ToLower(v.AuthMechanism)
		switch mechanism {
		case "cram-md5":
			v.auth = smtp.CRAMMD5Auth(v.Login, v.SenderPassword)
		case "login":
			v.auth = &loginAuth{[]byte(v.Login), []byte(v.SenderPassword)}
		case "", "plain":
			v.auth = smtp.PlainAuth("", v.Login, v.SenderPassword, v.SMTPAddr)
		default:
			return errors.New("unknown auth_mechanism")
		}
	}

	// Optionally resolve paths.
	v.ValidationTemplFile, err = validate.ResolveTemplatePath(v.ValidationTemplFile)
	if err != nil {
		return err
	}
	v.ResetTemplFile, err = validate.ResolveTemplatePath(v.ResetTemplFile)
	if err != nil {
		return err
	}

	// Paths to templates could be templates themselves: they may be language-dependent.
	var validationPathTempl, resetPathTempl *textt.Template
	validationPathTempl, err = textt.New("validation").Parse(v.ValidationTemplFile)
	if err != nil {
		return err
	}
	resetPathTempl, err = textt.New("reset").Parse(v.ResetTemplFile)
	if err != nil {
		return err
	}

	var path string
	if len(v.Languages) > 0 {
		v.validationTempl = make([]*textt.Template, len(v.Languages))
		v.resetTempl = make([]*textt.Template, len(v.Languages))
		var langTags []i18n.Tag
		// Find actual content templates for each defined language.
		for idx, lang := range v.Languages {
			tag, err := i18n.Parse(lang)
			if err != nil {
				return err
			}
			langTags = append(langTags, tag)
			if v.validationTempl[idx], path, err = validate.ReadTemplateFile(validationPathTempl, lang); err != nil {
				return err
			}
			if err = isTemplateValid(v.validationTempl[idx]); err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}

			if v.resetTempl[idx], path, err = validate.ReadTemplateFile(resetPathTempl, lang); err != nil {
				return err
			}
			if err = isTemplateValid(v.resetTempl[idx]); err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}
		}
		v.langMatcher = i18n.NewMatcher(langTags)
	} else {
		v.validationTempl = make([]*textt.Template, 1)
		v.resetTempl = make([]*textt.Template, 1)
		// No i18n support. Use defaults.
		v.validationTempl[0], path, err = validate.ReadTemplateFile(validationPathTempl, "")
		if err != nil {
			return err
		}
		if err = isTemplateValid(v.validationTempl[0]); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		v.resetTempl[0], path, err = validate.ReadTemplateFile(resetPathTempl, "")
		if err != nil {
			return err
		}
		if err = isTemplateValid(v.resetTempl[0]); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	}

	if v.HostUrl, err = validate.ValidateHostURL(v.HostUrl); err != nil {
		return err
	}

	if v.SMTPHeloHost == "" {
		hostUrl, _ := url.Parse(v.HostUrl)
		v.SMTPHeloHost = hostUrl.Hostname()
	}
	if v.SMTPHeloHost == "" {
		return errors.New("missing SMTP host")
	}

	if v.MaxRetries == 0 {
		v.MaxRetries = defaultMaxRetries
	}
	if v.CodeLength == 0 {
		v.CodeLength = defaultCodeLength
	}
	v.maxCodeValue = big.NewInt(0).Exp(big.NewInt(10), big.NewInt(int64(v.CodeLength)), nil)

	if v.SMTPPort == "" {
		v.SMTPPort = defaultPort
	}

	return nil
}

// IsInitialized returns true if the validator is initialized.
func (v *validator) IsInitialized() bool {
	return v.SMTPHeloHost != ""
}

// PreCheck validates the credential and parameters without sending an email.
// If the credential is valid, it's returned with an appropriate prefix.
func (v *validator) PreCheck(cred string, _ map[string]interface{}) (string, error) {
	if len(cred) > maxEmailLength {
		return "", t.ErrMalformed
	}

	// The email must be plain user@domain.
	addr, err := mail.ParseAddress(cred)
	if err != nil || addr.Address != cred {
		return "", t.ErrMalformed
	}

	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	addr.Address = strings.ToLower(addr.Address)

	// If a whitelist of domains is provided, make sure the email belongs to the list.
	if len(v.Domains) > 0 {
		// Parse email into user and domain parts.
		parts := strings.Split(addr.Address, "@")
		if len(parts) != 2 {
			return "", t.ErrMalformed
		}

		var found bool
		for _, domain := range v.Domains {
			if domain == parts[1] {
				found = true
				break
			}
		}

		if !found {
			return "", t.ErrPolicy
		}
	}

	return validatorName + ":" + addr.Address, nil
}

// Send a request for confirmation to the user: makes a record in DB and nothing else.
func (v *validator) Request(user t.Uid, email, lang, resp string, tmpToken []byte) (bool, error) {
	// Email validator cannot accept an immediate response.
	if resp != "" {
		return false, t.ErrFailed
	}

	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	email = strings.ToLower(email)

	token := make([]byte, base64.StdEncoding.EncodedLen(len(tmpToken)))
	base64.StdEncoding.Encode(token, tmpToken)

	// Generate expected response as a random numeric string between 0 and 999999.
	code, err := crand.Int(crand.Reader, v.maxCodeValue)
	if err != nil {
		return false, err
	}
	resp = strconv.FormatInt(code.Int64(), 10)
	resp = strings.Repeat("0", v.CodeLength-len(resp)) + resp

	var template *textt.Template
	if v.langMatcher != nil {
		_, idx := i18n.MatchStrings(v.langMatcher, lang)
		template = v.validationTempl[idx]
	} else {
		template = v.validationTempl[0]
	}

	content, err := validate.ExecuteTemplate(template, templateParts, map[string]interface{}{
		"Token":   url.QueryEscape(string(token)),
		"Code":    resp,
		"HostUrl": v.HostUrl})
	if err != nil {
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
	go v.send(email, content)

	return isNew, nil
}

// ResetSecret sends a message with instructions for resetting an authentication secret.
func (v *validator) ResetSecret(email, scheme, lang string, tmpToken []byte, params map[string]interface{}) error {
	// Normalize email to make sure Unicode case collisions don't lead to security problems.
	email = strings.ToLower(email)

	token := make([]byte, base64.StdEncoding.EncodedLen(len(tmpToken)))
	base64.StdEncoding.Encode(token, tmpToken)

	var template *textt.Template
	if v.langMatcher != nil {
		_, idx := i18n.MatchStrings(v.langMatcher, lang)
		template = v.resetTempl[idx]
	} else {
		template = v.resetTempl[0]
	}

	var login string
	if params != nil {
		// Invariant: params["login"] is a string. Will panic if the invariant doesn't hold.
		login = params["login"].(string)
	}

	content, err := validate.ExecuteTemplate(template, templateParts, map[string]interface{}{
		"Login":   login,
		"Token":   url.QueryEscape(string(token)),
		"Scheme":  scheme,
		"HostUrl": v.HostUrl})
	if err != nil {
		return err
	}

	// Send email without blocking. Email sending may take long time.
	go v.send(email, content)

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

// SendMail replacement
func (v *validator) sendMail(rcpt []string, msg []byte) error {

	client, err := smtp.Dial(v.SMTPAddr + ":" + v.SMTPPort)
	if err != nil {
		return err
	}
	defer client.Close()
	if err = client.Hello(v.SMTPHeloHost); err != nil {
		return err
	}
	if istls, _ := client.Extension("STARTTLS"); istls {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: v.TLSInsecureSkipVerify,
			ServerName:         v.SMTPAddr,
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if v.auth != nil {
		if isauth, _ := client.Extension("AUTH"); isauth {
			err = client.Auth(v.auth)
			if err != nil {
				return err
			}
		}
	}
	if err = client.Mail(strings.ReplaceAll(strings.ReplaceAll(v.senderEmail, "\r", " "), "\n", " ")); err != nil {
		return err
	}
	for _, to := range rcpt {
		if err = client.Rcpt(strings.ReplaceAll(strings.ReplaceAll(to, "\r", " "), "\n", " ")); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// This is a basic SMTP sender which connects to a server using login/password.
// -
// See here how to send email from Amazon SES:
// https://docs.aws.amazon.com/sdk-for-go/api/service/ses/#example_SES_SendEmail_shared00
// -
// Mailjet and SendGrid have some free email limits.
func (v *validator) send(to string, content map[string]string) error {
	message := &bytes.Buffer{}

	// Common headers.
	fmt.Fprintf(message, "From: %s\r\n", v.SendFrom)
	fmt.Fprintf(message, "To: %s\r\n", to)
	message.WriteString("Subject: ")
	// Old email clients may barf on UTF-8 strings.
	// Encode as quoted printable with 75-char strings separated by spaces, split by spaces, reassemble.
	message.WriteString(strings.Join(strings.Split(mime.QEncoding.Encode("utf-8", content["subject"]), " "), "\r\n    "))
	message.WriteString("\r\n")
	message.WriteString("MIME-version: 1.0;\r\n")

	if content["body_html"] == "" {
		// Plain text message
		message.WriteString("Content-Type: text/plain; charset=\"UTF-8\"; format=flowed; delsp=yes\r\n")
		message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b64w := base64.NewEncoder(base64.StdEncoding, message)
		b64w.Write([]byte(content["body_plain"]))
		b64w.Close()
	} else if content["body_plain"] == "" {
		// HTML-formatted message
		message.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		message.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qpw := qp.NewWriter(message)
		qpw.Write([]byte(content["body_html"]))
		qpw.Close()
	} else {
		// Multipart-alternative message includes both HTML and plain text components.
		boundary := randomBoundary()
		message.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

		message.WriteString("--" + boundary + "\r\n")
		message.WriteString("Content-Type: text/plain; charset=\"UTF-8\"; format=flowed; delsp=yes\r\n")
		message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b64w := base64.NewEncoder(base64.StdEncoding, message)
		b64w.Write([]byte(content["body_plain"]))
		b64w.Close()

		message.WriteString("\r\n")

		message.WriteString("--" + boundary + "\r\n")
		message.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		message.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qpw := qp.NewWriter(message)
		qpw.Write([]byte(content["body_html"]))
		qpw.Close()

		message.WriteString("\r\n--" + boundary + "--")
	}

	message.WriteString("\r\n")

	err := v.sendMail([]string{to}, message.Bytes())
	if err != nil {
		logs.Warn.Println("SMTP error", to, err)
	}

	return err
}

// Check if the template contains all required parts.
func isTemplateValid(templ *textt.Template) error {
	if templ.Lookup("subject") == nil {
		return fmt.Errorf("template invalid: '%s' not found", "subject")
	}
	if templ.Lookup("body_plain") == nil && templ.Lookup("body_html") == nil {
		return fmt.Errorf("template invalid: neither of '%s', '%s' is found", "body_plain", "body_html")
	}
	return nil
}

type loginAuth struct {
	username, password []byte
}

// Start begins an authentication with a server. Exported only to satisfy the interface definition.
func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

// Next continues the authentication. Exported only to satisfy the interface definition.
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch strings.ToLower(string(fromServer)) {
		case "username:":
			return a.username, nil
		case "password:":
			return a.password, nil
		default:
			return nil, fmt.Errorf("LOGIN AUTH unknown server response '%s'", string(fromServer))
		}
	}
	return nil, nil
}

func randomBoundary() string {
	var buf [24]byte
	rand.Read(buf[:])
	return fmt.Sprintf("tinode--%x", buf[:])
}

func init() {
	store.RegisterValidator(validatorName, &validator{})
}
