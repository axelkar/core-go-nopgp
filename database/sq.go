package database

import (
	"context"
	"fmt"
	"reflect"

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

// Prepares an UPDATE statement which applies the changes in the input map to
// the given model.
func Apply(m Model, input map[string]interface{}) sq.UpdateBuilder {
	// XXX: This relies on the GraphQL validator to prevent the user from
	// updating columns they're not supposed to. Risky?
	table := m.Table()
	if m.Alias() != "" {
		table += " " + m.Alias()
	}
	update := sq.Update(table).PlaceholderFormat(sq.Dollar)

	defer func() {
		// Some weird reflection errors don't get properly logged if they're
		// caught at a higher level.
		if err := recover(); err != nil {
			fmt.Printf("%v\n", err)
			panic(err)
		}
	}()

	for field, value := range input {
		fields, ok := m.Fields().GQL(field)
		if !ok {
			continue
		}
		if len(fields) != 1 {
			panic(fmt.Errorf("Apply cannot be used with composite fields"))
		}
		f := fields[0]

		var (
			pv reflect.Value = reflect.Indirect(reflect.ValueOf(f.Ptr))
			rv reflect.Value = reflect.ValueOf(value)
		)
		if pv.Type().Kind() == reflect.Ptr {
			if !rv.IsValid() {
				pv.Set(reflect.Zero(pv.Type()))
				update = update.Set(WithAlias(m.Alias(), f.SQL), nil)
			} else {
				if !pv.Elem().IsValid() {
					pv.Set(reflect.New(pv.Type().Elem()))
				}
				reflect.Indirect(pv).Set(reflect.Indirect(rv))
				update = update.Set(WithAlias(m.Alias(), f.SQL),
					reflect.Indirect(rv).Interface())
			}
		} else {
			if !rv.IsValid() {
				panic(fmt.Errorf("Invariant violated (unsetting non-nullable field)"))
			} else {
				pv.Set(rv)
				update = update.Set(WithAlias(m.Alias(), f.SQL), rv.Interface())
			}
		}
	}

	return update
}
