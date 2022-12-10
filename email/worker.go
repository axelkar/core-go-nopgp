package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	work "git.sr.ht/~sircmpwn/dowork"
	"github.com/ProtonMail/go-crypto/openpgp"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-pgpmail"
	"github.com/vaughan0/go-ini"
)

var emailCtxKey = &contextKey{"email"}

type contextKey struct {
	name string
}

// Returns a task which will send this email for the work queue. If the caller
// does not need to customize the task parameters, the Enqueue function may be
// more desirable.
func NewTask(msg *bytes.Buffer, rcpts []string) *work.Task {
	return work.NewTask(func(ctx context.Context) error {
		err := Send(ctx, msg, rcpts)
		if err != nil {
			log.Printf("Error sending mail: %v", err)
		}
		return err
	}).Retries(10).After(func(ctx context.Context, task *work.Task) {
		if task.Result() == nil {
			log.Printf("Mail to %s sent after %d attempts",
				strings.Join(rcpts, ", "), task.Attempts())
		} else {
			log.Printf("Mail to %s failed after %d attempts: %v",
				strings.Join(rcpts, ", "), task.Attempts(), task.Result())
		}
	})
}

func prepareEncrypted(rcptKey *string, header mail.Header,
	buf *bytes.Buffer, signed *openpgp.Entity) (io.WriteCloser, error) {
	keyring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(*rcptKey))
	if err != nil {
		return nil, err
	}
	if len(keyring) != 1 {
		return nil, errors.New("Expected user PGP key to contain one key")
	}
	rcptEntity := keyring[0]

	return pgpmail.Encrypt(buf, header.Header.Header,
		[]*openpgp.Entity{rcptEntity}, signed, nil)
}

func prepareSigned(header mail.Header, buf *bytes.Buffer,
	signed *openpgp.Entity) (io.WriteCloser, error) {
	result, err := pgpmail.Sign(buf, header.Header.Header, signed, nil)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Updates an email with the standard SourceHut headers, signs and optionally
// encrypts it, and then queues it for delivery.
//
// Senders should fill in at least the To and Subject headers, and the message
// body. Message-ID, Date, From, and Reply-To will also be added if they are not
// already present.
func EnqueueStd(ctx context.Context, header mail.Header,
	bodyReader io.Reader, rcptKey *string) error {

	queue := ForContext(ctx)

	to, err := header.AddressList("To")
	if err != nil {
		return fmt.Errorf("invalid To header field: %v", err)
	}
	cc, err := header.AddressList("Cc")
	if err != nil {
		return fmt.Errorf("invalid Cc header field: %v", err)
	}

	var rcpts []string
	for _, addr := range to {
		rcpts = append(rcpts, addr.Address)
	}
	for _, addr := range cc {
		rcpts = append(rcpts, addr.Address)
	}

	if !header.Has("Message-Id") {
		header.GenerateMessageID()
	}
	if !header.Has("Date") {
		header.SetDate(time.Now().UTC())
	}
	if !header.Has("From") {
		header.SetAddressList("From", []*mail.Address{queue.smtpFrom})
	}
	if !header.Has("Reply-To") {
		header.SetAddressList("Reply-To", []*mail.Address{queue.ownerAddress})
	}

	var (
		buf       bytes.Buffer
		cleartext io.WriteCloser
	)

	if rcptKey != nil {
		cleartext, err = prepareEncrypted(rcptKey, header, &buf, queue.entity)
	}
	// Fall back to unencrypted email if encryption did not work
	// TODO should we add the error message to the email?
	if rcptKey == nil || err != nil {
		if err != nil {
			buf.Reset()
			log.Printf("Encrypting mail to %s failed: %s",
				strings.Join(rcpts, ", "), err.Error())
		}

		cleartext, err = prepareSigned(header, &buf, queue.entity)
		if err != nil {
			log.Fatalf("Signing mail failed: %v", err)
		}
	}
	defer cleartext.Close()

	var inlineHeader mail.Header
	inlineHeader.SetContentType("text/plain", nil)
	body, err := mail.CreateSingleInlineWriter(cleartext, inlineHeader)
	if err != nil {
		panic(err)
	}
	defer body.Close()

	_, err = io.Copy(body, bodyReader)
	if err != nil {
		log.Fatal(err)
	}

	return queue.Enqueue(NewTask(&buf, rcpts))
}

type Queue struct {
	*work.Queue
	smtpFrom     *mail.Address
	ownerAddress *mail.Address
	entity       *openpgp.Entity
}

// Creates a new email processing queue.
func NewQueue(conf ini.File) *Queue {
	smtpFrom, ok := conf.Get("mail", "smtp-from")
	if !ok {
		panic("Expected [mail]smtp-from in config")
	}
	ownerName, ok := conf.Get("sr.ht", "owner-name")
	if !ok {
		panic("Expected [sr.ht]owner-name in config")
	}
	ownerEmail, ok := conf.Get("sr.ht", "owner-email")
	if !ok {
		panic("Expected [sr.ht]owner-email in config")
	}
	addr, err := mail.ParseAddress(smtpFrom)
	if err != nil {
		panic(err)
	}
	ownerAddr := &mail.Address{
		Name:    ownerName,
		Address: ownerEmail,
	}

	privKeyPath, ok := conf.Get("mail", "pgp-privkey")
	if !ok {
		panic("Expected [mail]pgp-privkey in config")
	}

	privKeyFile, err := os.Open(privKeyPath)
	if err != nil {
		panic(fmt.Errorf("Failed to open [mail]pgp-privkey: %v", err))
	}
	defer privKeyFile.Close()

	keyring, err := openpgp.ReadArmoredKeyRing(privKeyFile)
	if err != nil {
		panic(fmt.Errorf("Failed to read PGP key ring from [mail]pgp-privkey: %v", err))
	}
	if len(keyring) != 1 {
		panic("Expected [mail]pgp-privkey to contain one key")
	}
	entity := keyring[0]
	if entity.PrivateKey == nil || entity.PrivateKey.Encrypted {
		panic("Failed to load [mail]pgp-privkey for email signature")
	}

	return &Queue{
		Queue:        work.NewQueue("email"),
		smtpFrom:     addr,
		ownerAddress: ownerAddr,
		entity:       entity,
	}
}

// Returns the email worker for this context.
func ForContext(ctx context.Context) *Queue {
	q, ok := ctx.Value(emailCtxKey).(*Queue)
	if !ok {
		panic(errors.New("No email worker for this context"))
	}
	return q
}

// Returns a context which includes the given mail worker.
func Context(ctx context.Context, queue *Queue) context.Context {
	return context.WithValue(ctx, emailCtxKey, queue)
}

// Adds HTTP middleware to provide an email work queue to this context.
func Middleware(queue *Queue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(Context(r.Context(), queue))
			next.ServeHTTP(w, r)
		})
	}
}
