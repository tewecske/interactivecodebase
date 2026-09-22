// Package mail delivers mail over SMTP.
package mail

import (
	"context"
	"net/smtp"

	"example.com/webapp/internal/notes"
)

// SMTPMailer sends mail through an SMTP relay.
type SMTPMailer struct {
	addr string
	from string
}

var _ notes.Mailer = (*SMTPMailer)(nil)

// NewSMTPMailer returns a mailer for the relay at addr.
func NewSMTPMailer(addr, from string) *SMTPMailer {
	return &SMTPMailer{addr: addr, from: from}
}

// Send delivers one plain-text message.
func (m *SMTPMailer) Send(_ context.Context, to, subject, body string) error {
	msg := []byte("Subject: " + subject + "\r\n\r\n" + body)
	return smtp.SendMail(m.addr, nil, m.from, []string{to}, msg)
}
