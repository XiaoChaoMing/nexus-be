package mailer

import (
	"net/mail"
	"time"
)

// BuildMIMEForTest exposes buildMIME to the external test package.
func BuildMIMEForTest(from string, msg Message) ([]byte, error) {
	f, err := mail.ParseAddress(from)
	if err != nil {
		return nil, err
	}

	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return nil, err
	}

	return buildMIME(f, to, msg, time.Now())
}
