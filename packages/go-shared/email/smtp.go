package email

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
)

// SMTPConfig configures a [SMTPSender]. All fields are required
// except Username/Password, which may be empty for unauthenticated
// relays (e.g. a LAN-only mailhog in development).
type SMTPConfig struct {
	// Host is the SMTP server hostname (no port).
	Host string
	// Port is the SMTP server port (typically 587 for STARTTLS).
	Port int
	// Username is the SASL login. Empty to skip authentication.
	Username string
	// Password is the SASL secret.
	Password string
	// From is the default envelope sender used when a [Message] does
	// not specify one.
	From string
	// Brand is shown in the header of the HTML alternative.
	Brand Brand
}

// Brand is the deployment identity rendered in the HTML header. Only
// https image URLs are shown; data: URIs are ignored.
type Brand struct {
	Name    string
	LogoURL string
	IconURL string
}

// SMTPSender is a [Sender] backed by net/smtp. It uses PLAIN auth
// over the default Dialer (STARTTLS negotiation is handled by
// smtp.SendMail when the server advertises the extension).
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender constructs an SMTPSender from config. It validates
// required fields eagerly so misconfiguration surfaces at startup
// instead of the first Send call.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" {
		return nil, errors.New("email/smtp: Host is required")
	}
	if cfg.Port == 0 {
		return nil, errors.New("email/smtp: Port is required")
	}
	if cfg.From == "" {
		return nil, errors.New("email/smtp: From is required")
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send implements [Sender]. It formats m as an RFC 5322 message with
// the minimal headers inbound parsers need for reply attribution
// (From / To / Subject / Reply-To) and hands it to smtp.SendMail.
func (s *SMTPSender) Send(_ context.Context, m Message) error {
	if len(m.To) == 0 {
		return errors.New("email/smtp: Message.To is empty")
	}
	from := m.From
	if from == "" {
		from = s.cfg.From
	}
	headerValues := make([]string, 0, len(m.To)+3)
	headerValues = append(headerValues, from)
	headerValues = append(headerValues, m.To...)
	headerValues = append(headerValues, m.ReplyTo, m.Subject)
	if err := validateHeaderValues(headerValues...); err != nil {
		return err
	}
	envelopeFrom := envelopeAddress(from)
	envelopeTo := make([]string, 0, len(m.To))
	for _, to := range m.To {
		envelopeTo = append(envelopeTo, envelopeAddress(to))
	}
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", encodeAddressHeader(from))
	fmt.Fprintf(&b, "To: %s\r\n", encodeAddressListHeader(m.To))
	if m.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", encodeAddressHeader(m.ReplyTo))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(m.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	mw := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", mw.Boundary())
	for _, part := range []struct{ ctype, body string }{
		{"text/plain", m.Body},
		{"text/html", renderHTML(s.cfg.Brand, m.Body)},
	} {
		if err := writeQPPart(mw, part.ctype, part.body); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}

	return smtp.SendMail(addr, auth, envelopeFrom, envelopeTo, []byte(b.String()))
}

func writeQPPart(mw *multipart.Writer, ctype, body string) error {
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", ctype+"; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "quoted-printable")
	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	qw := quotedprintable.NewWriter(w)
	if _, err := qw.Write([]byte(body)); err != nil {
		return err
	}
	return qw.Close()
}

func validateHeaderValues(values ...string) error {
	for _, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("email/smtp: header values must not contain CR or LF")
		}
	}
	return nil
}

func encodeHeader(value string) string {
	return mime.QEncoding.Encode("utf-8", value)
}

func encodeAddressListHeader(values []string) string {
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		encoded = append(encoded, encodeAddressHeader(value))
	}
	return strings.Join(encoded, ", ")
}

func encodeAddressHeader(value string) string {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return encodeHeader(value)
	}
	if addr.Name == "" {
		return addr.Address
	}
	return encodeHeader(addr.Name) + " <" + addr.Address + ">"
}

func envelopeAddress(value string) string {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return value
	}
	return addr.Address
}

// compile-time check
var _ Sender = (*SMTPSender)(nil)
