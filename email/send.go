package email

import (
	"context"
	"fmt"
	"io"
	"net/mail"
	"strconv"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"git.sr.ht/~sircmpwn/core-go/config"
)

// Sends an email. Blocks until it's sent or an error occurs.
func Send(ctx context.Context, msg io.Reader, rcpts []string) error {
	conf := config.ForContext(ctx)

	portStr, ok := conf.Get("mail", "smtp-port")
	if !ok {
		panic(fmt.Errorf("[mail]smtp-port unset"))
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		panic(fmt.Errorf("Unable to parse [mail]smtp-port (must be integer)"))
	}

	host, ok1 := conf.Get("mail", "smtp-host")
	user, ok2 := conf.Get("mail", "smtp-user")
	pass, ok3 := conf.Get("mail", "smtp-password")
	from, ok4 := conf.Get("mail", "smtp-from")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		panic(fmt.Errorf("Missing SMTP configuration options"))
	}

	sender, err := mail.ParseAddress(from)
	if err != nil {
		panic(err)
	}

	auth := sasl.NewPlainClient("", user, pass)
	return smtp.SendMail(fmt.Sprintf("%s:%d", host, port),
		auth, sender.Address, rcpts, msg)
}
