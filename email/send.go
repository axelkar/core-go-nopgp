package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/martinlindhe/base36"
	gomail "gopkg.in/mail.v2"

	"git.sr.ht/~sircmpwn/core-go/config"
)

var attempts int = 0

// Sends an email. Blocks until it's sent or an error occurs.
func Send(ctx context.Context, m *gomail.Message) error {
	conf := config.ForContext(ctx)

	portStr, ok := conf.Get("mail", "smtp-port")
	if !ok {
		return errors.New("internal system error")
	}
	port, _ := strconv.Atoi(portStr)
	host, _ := conf.Get("mail", "smtp-host")
	user, _ := conf.Get("mail", "smtp-user")
	pass, _ := conf.Get("mail", "smtp-password")

	m.SetHeader("Message-ID", generateMessageID())
	m.SetDateHeader("Date", time.Now().UTC())

	d := gomail.NewDialer(host, port, user, pass)
	return d.DialAndSend(m)
}

// Generates an RFC 2822-compliant Message-Id based on the informational draft
// "Recommendations for generating Message IDs", for lack of a better
// authoritative source.
func generateMessageID() string {
	var (
		now   bytes.Buffer
		nonce []byte = make([]byte, 8)
	)
	binary.Write(&now, binary.BigEndian, time.Now().UnixNano())
	rand.Read(nonce)
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	return fmt.Sprintf("<%s.%s@%s>",
		base36.EncodeBytes(now.Bytes()),
		base36.EncodeBytes(nonce),
		hostname)
}
