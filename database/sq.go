package database

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// Provides a mapping between PostgreSQL columns, GQL fields, and Go struct
// fields for all of the data associated with a model.
type FieldMap struct {
	SQL string
	GQL string
	Ptr interface{}
}

type ModelFields struct {
	Fields []*FieldMap

	byGQL map[string][]*FieldMap
	bySQL map[string][]*FieldMap
	anon  []*FieldMap
}

func (mf *ModelFields) buildCache() {
	if mf.byGQL != nil && mf.bySQL != nil {
		return
	}

	mf.byGQL = make(map[string][]*FieldMap)
	mf.bySQL = make(map[string][]*FieldMap)
	for _, f := range mf.Fields {
		if f.GQL != "" {
			if _, ok := mf.byGQL[f.GQL]; !ok {
				mf.byGQL[f.GQL] = nil
			}
			mf.byGQL[f.GQL] = append(mf.byGQL[f.GQL], f)
		} else {
			mf.anon = append(mf.anon, f)
		}
		if _, ok := mf.bySQL[f.SQL]; !ok {
			mf.bySQL[f.SQL] = nil
		}
		mf.bySQL[f.SQL] = append(mf.bySQL[f.SQL], f)
	}
}

func (mf *ModelFields) GQL(name string) ([]*FieldMap, bool) {
	mf.buildCache()
	if f, ok := mf.byGQL[name]; !ok {
		return nil, false
	} else {
		return f, true
	}
}

func (mf *ModelFields) SQL(name string) ([]*FieldMap, bool) {
	mf.buildCache()
	if f, ok := mf.bySQL[name]; !ok {
		return nil, false
	} else {
		return f, true
	}
}

func (mf *ModelFields) All() []*FieldMap {
	return mf.Fields
}

func (mf *ModelFields) Anonymous() []*FieldMap {
	mf.buildCache()
	return mf.anon
}

type Model interface {
	Alias() string
	Fields() *ModelFields
	Table() string
}

type ExtendedModel interface {
	Model
	Select(q sq.SelectBuilder) sq.SelectBuilder
}

func Select(ctx context.Context, cols ...interface{}) sq.SelectBuilder {
	q := sq.Select().PlaceholderFormat(sq.Dollar)
	for _, col := range cols {
		switch col := col.(type) {
		case string:
			q = q.Columns(col)
		case []string:
			q = q.Columns(col...)
		case Model:
			if em, ok := col.(ExtendedModel); ok {
				q = em.Select(q.Columns(Columns(ctx, col)...))
			} else {
				q = q.Columns(Columns(ctx, col)...)
			}
		default:
			panic(fmt.Errorf("Unknown selectable type %T", col))
		}
	}
	return q
}
