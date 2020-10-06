package database

import (
	"context"
	"sort"

	"github.com/lib/pq"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/99designs/gqlgen/graphql"
)

func collectFields(ctx context.Context) []graphql.CollectedField {
	var fields []graphql.CollectedField
	if graphql.GetFieldContext(ctx) != nil {
		fields = graphql.CollectFieldsCtx(ctx, nil)

		octx := graphql.GetOperationContext(ctx)
		for _, col := range fields {
			if col.Name == "results" {
				// This endpoint is using the cursor pattern; the columns we
				// actually need to filter with are nested into the results
				// field.
				fields = graphql.CollectFields(octx, col.SelectionSet, nil)
				break
			}
		}
	}
	return fields
}

func Scan(ctx context.Context, m Model) []interface{} {
	qlFields := collectFields(ctx)
	if len(qlFields) == 0 {
		// Collect all fields if we are not in an active graphql context
		for _, field := range m.Fields().All() {
			qlFields = append(qlFields, graphql.CollectedField{
				&ast.Field{Name: field.GQL}, nil,
			})
		}
	}

	sort.Slice(qlFields, func(a, b int) bool {
		return qlFields[a].Name < qlFields[b].Name
	})

	var fields []interface{}
	for _, qlField := range qlFields {
		if field, ok := m.Fields().GQL(qlField.Name); ok {
			fields = append(fields, field.Ptr)
		}
	}

	for _, field := range m.Fields().Anonymous() {
		fields = append(fields, field.Ptr)
	}

	return fields
}

func Columns(ctx context.Context, m Model) []string {
	fields := collectFields(ctx)
	if len(fields) == 0 {
		// Collect all fields if we are not in an active graphql context
		for _, field := range m.Fields().All() {
			fields = append(fields, graphql.CollectedField{
				&ast.Field{Name: field.GQL}, nil,
			})
		}
	}

	sort.Slice(fields, func(a, b int) bool {
		return fields[a].Name < fields[b].Name
	})

	var columns []string
	for _, gql := range fields {
		if field, ok := m.Fields().GQL(gql.Name); ok {
			columns = append(columns, WithAlias(m.Alias(), field.SQL))
		}
	}

	for _, field := range m.Fields().Anonymous() {
		columns = append(columns, WithAlias(m.Alias(), field.SQL))
	}

	return columns
}

func WithAlias(alias, col string) string {
	if alias != "" {
		return pq.QuoteIdentifier(alias) + "." + pq.QuoteIdentifier(col)
	} else {
		return pq.QuoteIdentifier(col)
	}
}
