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

type mailConfig struct {
	host string
	port int
	from string

	enctype  string
	authtype string

	user string
	pass string
}

func mailSetup(ctx context.Context) (*smtp.Client, *mail.Address, error) {
	conf := config.ForContext(ctx)
	mailconf := &mailConfig{enctype: "starttls", authtype: "plain"}
	var err error

	portStr, ok := conf.Get("mail", "smtp-port")
	if !ok {
		panic(fmt.Errorf("[mail]smtp-port unset"))
	}
	mailconf.port, err = strconv.Atoi(portStr)
	if err != nil {
		panic(fmt.Errorf("Unable to parse [mail]smtp-port (must be integer)"))
	}

	if mailconf.host, ok = conf.Get("mail", "smtp-host"); !ok {
		panic(fmt.Errorf("Missing SMTP configuration options [smtp-host]"))
	}
	if mailconf.from, ok = conf.Get("mail", "smtp-from"); !ok {
		panic(fmt.Errorf("Missing SMTP configuration options [smtp-from]"))
	}

	sender, err := mail.ParseAddress(mailconf.from)
	if err != nil {
		panic(err)
	}

	if enctype, ok := conf.Get("mail", "smtp-encryption"); ok {
		switch enctype {
		case "starttls", "tls", "insecure":
			mailconf.enctype = enctype
		default:
			panic(fmt.Errorf("Invalid SMTP configuration value for [smtp-encryption]"))
		}
	}

	if authtype, ok := conf.Get("mail", "smtp-auth"); ok {
		switch authtype {
		case "none", "plain":
			mailconf.authtype = authtype
		default:
			panic(fmt.Errorf("Invalid SMTP configuration value for [smtp-auth]"))
		}
	}
	if mailconf.authtype == "plain" {
		if mailconf.user, ok = conf.Get("mail", "smtp-user"); !ok {
			panic(fmt.Errorf("Missing SMTP configuration options [smtp-user]"))
		}
		if mailconf.pass, ok = conf.Get("mail", "smtp-password"); !ok {
			panic(fmt.Errorf("Missing SMTP configuration options [smtp-password]"))
		}
	}

	var c *smtp.Client
	addr := fmt.Sprintf("%s:%d", mailconf.host, mailconf.port)

	if mailconf.enctype == "tls" {
		c, err = smtp.DialTLS(addr, nil)
	} else {
		c, err = smtp.Dial(addr)
	}
	if err != nil {
		return nil, nil, err
	}

	if err = c.Hello("localhost"); err != nil {
		return nil, nil, err
	}

	if mailconf.enctype == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			panic(fmt.Errorf("smtp: server doesn't support STARTTLS"))
		}
		if err = c.StartTLS(nil); err != nil {
			return nil, nil, err
		}
	}

	if mailconf.authtype == "plain" {
		auth := sasl.NewPlainClient("", mailconf.user, mailconf.pass)
		if ok, _ := c.Extension("AUTH"); !ok {
			panic(fmt.Errorf("smtp: server doesn't support AUTH"))
		}
		if err = c.Auth(auth); err != nil {
			return nil, nil, err
		}
	}

	return c, sender, nil
}

// Sends an email. Blocks until it's sent or an error occurs.
func Send(ctx context.Context, msg io.Reader, rcpts []string) error {
	c, sender, err := mailSetup(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.SendMail(sender.Address, rcpts, msg)
}
