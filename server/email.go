package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"runtime"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vaughan0/go-ini"
	gomail "gopkg.in/mail.v2"

	"git.sr.ht/~sircmpwn/core-go/auth"
	"git.sr.ht/~sircmpwn/core-go/email"
)

// Provides a graphql.RecoverFunc which will print the stack trace, and if
// debug mode is not enabled, email it to the administrator.
func EmailRecover(config ini.File, debug bool, srv string) graphql.RecoverFunc {
	return func(ctx context.Context, _origErr interface{}) error {
		var (
			ok      bool
			origErr error
		)
		if origErr, ok = _origErr.(error); !ok {
			log.Printf("Unexpected error in recover: %v\n", origErr)
			return fmt.Errorf("internal system error")
		}

		if errors.Is(origErr, context.Canceled) {
			return origErr
		}

		if errors.Is(origErr, context.DeadlineExceeded) {
			return origErr
		}

		if origErr.Error() == "pq: canceling statement due to user request" {
			return origErr
		}

		stack := make([]byte, 32768) // 32 KiB
		i := runtime.Stack(stack, false)
		log.Println(origErr.Error())
		log.Println(string(stack[:i]))
		if debug {
			return fmt.Errorf("internal system error")
		}

		to, ok := config.Get("mail", "error-to")
		if !ok {
			return fmt.Errorf("internal system error")
		}
		from, _ := config.Get("mail", "error-from")

		m := gomail.NewMessage()
		sender, err := mail.ParseAddress(from)
		if err != nil {
			log.Fatalf("Failed to parse sender address")
		}
		m.SetAddressHeader("From", sender.Address, sender.Name)
		recipient, err := mail.ParseAddress(to)
		if err != nil {
			log.Fatalf("Failed to parse recipient address")
		}
		m.SetAddressHeader("To", recipient.Address, recipient.Name)
		m.SetHeader("Subject", fmt.Sprintf(
			"[%s] GraphQL query error: %v", srv, origErr))

		quser := auth.ForContext(ctx)
		octx := graphql.GetOperationContext(ctx)

		m.SetBody("text/plain", fmt.Sprintf(`Error occured processing GraphQL request:

%v

When running the following query on behalf of %s <%s>:

%s

The following stack trace was produced:

%s`, origErr, quser.Username, quser.Email, octx.RawQuery, string(stack[:i])))

		email.Enqueue(ctx, m)
		return fmt.Errorf("internal system error")
	}
}
