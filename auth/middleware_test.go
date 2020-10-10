package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/vaughan0/go-ini"

	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/database"
)

func TestNoAuthorization(t *testing.T) {
	mw, _, next := middleware()
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")
	resp := &TestResponse{T: t}
	mw(resp, req)
	assert.False(t, *next)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestCookie(t *testing.T) {
	mw, subctx, next := middleware()
	ctx, mock := dbctx()
	mockUserLookup(mock)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")

	cookie := AuthCookie{
		Name: "jdoe",
	}
	payload, err := json.Marshal(&cookie)
	assert.Nil(t, err)

	req.AddCookie(&http.Cookie{
		Name:  "sr.ht.unified-login.v1",
		Value: string(crypto.Encrypt(payload)),
	})

	resp := &TestResponse{T: t}
	mw(resp, req)
	assert.True(t, *next)
	assert.Nil(t, mock.ExpectationsWereMet())

	auth := ForContext(*subctx)
	assert.Equal(t, auth.AuthMethod, AUTH_COOKIE)
	assert.Equal(t, auth.UserID, 1337)
	assert.Equal(t, auth.Username, "jdoe")
	assert.Equal(t, auth.Email, "jdoe@example.org")

	// Test that invalid cookie fails
	ctx, _ = dbctx()
	req, err = http.NewRequestWithContext(ctx, "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")

	req.AddCookie(&http.Cookie{
		Name:  "sr.ht.unified-login.v1",
		Value: string("Invalid auth cookie"),
	})

	*next = false
	resp = &TestResponse{T: t}
	mw(resp, req)
	assert.False(t, *next)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestInternal(t *testing.T) {
	mw, subctx, next := middleware()
	ctx, mock := dbctx()
	mockUserLookup(mock)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")
	internalAuth := InternalAuth{
		Name:            "jdoe",
		ClientID:        "",
		NodeID:          "test.node",
		OAuthClientUUID: "",
	}
	payload, err := json.Marshal(&internalAuth)
	assert.Nil(t, err)
	req.Header.Add("Authorization", "Internal "+
		string(crypto.Encrypt(payload)))
	req.RemoteAddr = "127.0.0.1"

	resp := &TestResponse{T: t}
	mw(resp, req)
	assert.True(t, *next)
	assert.Nil(t, mock.ExpectationsWereMet())

	auth := ForContext(*subctx)
	assert.Equal(t, auth.AuthMethod, AUTH_INTERNAL)
	assert.Equal(t, auth.UserID, 1337)
	assert.Equal(t, auth.Username, "jdoe")
	assert.Equal(t, auth.Email, "jdoe@example.org")

	// Expect failure when outside of internal IP network
	ctx, _ = dbctx()
	req, err = http.NewRequestWithContext(ctx, "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "Internal "+
		string(crypto.Encrypt(payload)))
	req.RemoteAddr = "1.2.3.4"

	*next = false
	resp = &TestResponse{T: t}
	mw(resp, req)
	assert.False(t, *next)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Expect failure with invalid header
	ctx, _ = dbctx()
	req, err = http.NewRequestWithContext(ctx, "POST",
		"https://example.org/query",
		strings.NewReader(`{"query": "query { me { id } }"}`))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "Internal fakeauth")
	req.RemoteAddr = "127.0.0.1"

	*next = false
	resp = &TestResponse{T: t}
	mw(resp, req)
	assert.False(t, *next)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

var conf ini.File

func init() {
	var err error
	conf, err = ini.Load(strings.NewReader(`
[webhooks]
private-key=ebzsjPaN6E13ln/FeNWly1C92q6bVMVdOnDo1HPl5fc=

[sr.ht]
network-key=tbuG-7Vh44vrDq1L_HKWkHnWrDOtJhEkPKPiauaLeuk=

[test::api]
internal-ipnet=127.0.0.1/24,::1/64`))
	if err != nil {
		panic(err)
	}
	crypto.InitCrypto(conf)
}

func middleware() (http.HandlerFunc, *context.Context, *bool) {
	called := false
	var ctx context.Context
	next := (func(called *bool, ctx *context.Context) http.HandlerFunc {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*called = true
			*ctx = r.Context()
		})
	})(&called, &ctx)
	return Middleware(conf, "test::api")(next).ServeHTTP, &ctx, &called
}

func dbctx() (context.Context, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		panic(err)
	}
	ctx := database.Context(context.Background(), db)
	return ctx, mock
}

func mockUserLookup(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT`).
		WithArgs("jdoe").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "username", "created", "updated", "email", "user_type",
			"url", "location", "bio", "suspension_notice",
		}).
			AddRow(1337, "jdoe", time.Now().UTC(), time.Now().UTC(),
				"jdoe@example.org", "active_paying",
				"https://example.org", nil, nil, nil))
	mock.ExpectCommit()
}

type TestResponse struct {
	T *testing.T

	RespHeader http.Header
	Payload    []byte
	StatusCode int
}

func (tr *TestResponse) Header() http.Header {
	if tr.RespHeader == nil {
		tr.RespHeader = make(http.Header)
	}
	return tr.RespHeader
}

func (tr *TestResponse) Write(payload []byte) (int, error) {
	if tr.StatusCode == 0 {
		tr.WriteHeader(http.StatusOK)
	}

	assert.Nil(tr.T, tr.Payload)
	tr.Payload = payload
	return len(payload), nil
}

func (tr *TestResponse) WriteHeader(statusCode int) {
	assert.Zero(tr.T, tr.StatusCode)
	tr.StatusCode = statusCode
}
