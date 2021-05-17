package database

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

var dbCtxKey = &contextKey{"database"}

type contextKey struct {
	name string
}

func Middleware(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := Context(r.Context(), db)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func Context(ctx context.Context, db *sql.DB) context.Context {
	return context.WithValue(ctx, dbCtxKey, db)
}

func ForContext(ctx context.Context) (*sql.Conn, error) {
	raw, ok := ctx.Value(dbCtxKey).(*sql.DB)
	if !ok {
		panic(errors.New("Invalid database context"))
	}
	return raw.Conn(ctx)
}

func DBForContext(ctx context.Context) *sql.DB {
	raw, ok := ctx.Value(dbCtxKey).(*sql.DB)
	if !ok {
		panic(errors.New("Invalid database context"))
	}
	return raw
}

func WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error {
	conn, err := ForContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	err = fn(tx)
	if err != nil {
		err := tx.Rollback()
		if err != nil && err != sql.ErrTxDone {
			panic(err)
		}
	} else {
		err := tx.Commit()
		if err != nil && err != sql.ErrTxDone {
			panic(err)
		}
	}
	return err
}
