package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	gomail "net/mail"
	"runtime"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/emersion/go-message/mail"

	"git.sr.ht/~sircmpwn/core-go/auth"
	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/email"
)

// Provides a graphql.RecoverFunc which will print the stack trace, and if
// debug mode is not enabled, email it to the administrator.
func EmailRecover(ctx context.Context, _origErr interface{}) error {
	log.Println(_origErr)
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
	if config.Debug {
		return fmt.Errorf("internal system error")
	}

	var header mail.Header
	header.SetSubject(fmt.Sprintf("[%s] GraphQL query error: %v",
		config.ServiceName(ctx), origErr))

	conf := config.ForContext(ctx)
	to, ok := conf.Get("mail", "error-to")
	if !ok {
		return fmt.Errorf("internal system error")
	}
	rcpt, err := gomail.ParseAddress(to)
	if err != nil {
		panic(errors.New("Failed to parse sender address"))
	}
	addr := mail.Address(*rcpt)
	header.SetAddressList("To", []*mail.Address{&addr})

	var reader io.Reader
	func() {
		defer func() {
			if err := recover(); err != nil {
				reader = strings.NewReader(fmt.Sprintf(`An error occured outside of the GraphQL context:

				%s`, string(stack[:i])))
			}
		}()
		quser := auth.ForContext(ctx)
		octx := graphql.GetOperationContext(ctx)
		vars, err := json.Marshal(octx.Variables)
		if err != nil {
			vars = []byte{}[:]
		}
		reader = strings.NewReader(
			fmt.Sprintf(`Error occured processing GraphQL request:

%v

When running the following query on behalf of %s <%s>:

%s

With these variables:

%s

The following stack trace was produced:

%s`, origErr, quser.Username, quser.Email, octx.RawQuery,
				string(vars), string(stack[:i])))
	}()

	email.EnqueueStd(ctx, header, reader, nil)
	return fmt.Errorf("internal system error")
}
