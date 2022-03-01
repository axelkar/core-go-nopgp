package valid

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type Validation struct {
	ctx   context.Context
	input map[string]interface{}
}

type ValidationError struct {
	valid *Validation
	err   *gqlerror.Error
}

// Returns a new GraphQL error attached to the given field.
func Error(ctx context.Context, field string, msg string) error {
	return &gqlerror.Error{
		Message: msg,
		Path:    graphql.GetPath(ctx),
		Extensions: map[string]interface{}{
			"field": field,
		},
	}
}

// Returns a new GraphQL error attached to the given field.
func Errorf(ctx context.Context, field string, msg string, items ...interface{}) error {
	return &gqlerror.Error{
		Message: fmt.Sprintf(msg, items...),
		Path:    graphql.GetPath(ctx),
		Extensions: map[string]interface{}{
			"field": field,
		},
	}
}

// Creates a new validation context.
func New(ctx context.Context) *Validation {
	return &Validation{
		ctx: ctx,
	}
}

// Adds an input map to a validation context.
func (valid *Validation) WithInput(input map[string]interface{}) *Validation {
	valid.input = input
	return valid
}

// Returns true if no errors were found.
func (valid *Validation) Ok() bool {
	return len(graphql.GetErrors(valid.ctx)) == 0
}

// Fetches an item from the validation context, which must have an input
// registered. If the field is not present, the callback is not run. Otherwise,
// the function is called with the value for the user to conduct further
// validation with.
func (valid *Validation) Optional(name string, fn func(i interface{})) {
	if valid.input == nil {
		panic("Attempted to validate fields without input")
	}
	if o, ok := valid.input[name]; ok {
		if o == nil {
			return
		}
		fn(o)
	}
}

// Fetches a string from the validation context, which must have an input
// registered. If the field is not present, the callback is not run. If
// present, but not a string, an error is recorded. Otherwise, the function is
// called with the string for the user to conduct further validation with.
func (valid *Validation) OptionalString(name string, fn func(s string)) {
	if valid.input == nil {
		panic("Attempted to validate fields without input")
	}
	if o, ok := valid.input[name]; ok {
		if o == nil {
			return
		}
		var val string
		switch s := o.(type) {
		case string:
			val = s
		case *string:
			val = *s
		default:
			valid.
				Error("Expected %s to be a string", name).
				WithField(name)
			return
		}
		fn(val)
	}
}

// Fetches a nullable string from the validation context, which must have an
// input registered. If the field is not present, the callback is not run. If
// present, but null, the function is called with null set to true. Otherwise,
// the function is called with the string for the user to conduct further
// validation with.
func (valid *Validation) NullableString(name string, fn func(s *string)) {
	if valid.input == nil {
		panic("Attempted to validate fields without input")
	}
	if o, ok := valid.input[name]; ok {
		var val *string
		if o != nil {
			switch s := o.(type) {
			case string:
				val = &s
			case *string:
				val = s
			default:
				valid.
					Error("Expected %s to be a string", name).
					WithField(name)
				return
			}
		}
		fn(val)
	}
}

// Fetches a boolean from the validation context, which must have an input
// registered. If the field is not present, the callback is not run. If
// present, but not a boolean, an error is recorded. Otherwise, the function is
// called with the boolean for the user to conduct further validation with.
func (valid *Validation) OptionalBool(name string, fn func(b bool)) {
	if valid.input == nil {
		panic("Attempted to validate fields without input")
	}
	if o, ok := valid.input[name]; ok {
		if o == nil {
			return
		}
		var val bool
		switch b := o.(type) {
		case bool:
			val = b
		case *bool:
			val = *b
		default:
			valid.
				Error("Expected %s to be a bool", name).
				WithField(name)
			return
		}
		fn(val)
	}
}

// Creates a validation error unconditionally.
func (valid *Validation) Error(msg string,
	items ...interface{}) *ValidationError {
	err := &gqlerror.Error{
		Path:    graphql.GetPath(valid.ctx),
		Message: fmt.Sprintf(msg, items...),
	}
	graphql.AddError(valid.ctx, err)
	return &ValidationError{
		valid: valid,
		err:   err,
	}
}

// Asserts that a condition is true, recording a GraphQL error with the given
// message if not.
func (valid *Validation) Expect(cond bool,
	msg string, items ...interface{}) *ValidationError {
	if cond {
		return &ValidationError{valid: valid}
	}
	return valid.Error(msg, items...)
}

// Associates a field name with an error.
func (err *ValidationError) WithField(field string) *ValidationError {
	if err.err == nil {
		return err
	}
	if err.err.Extensions == nil {
		err.err.Extensions = make(map[string]interface{})
	}
	err.err.Extensions["field"] = field
	return err
}

// Composes another assertion onto the same validation context which initially
// created an error. Short-circuiting is used, such that if the earlier
// condition failed, the new condition is not considered.
func (err *ValidationError) And(cond bool,
	msg string, items ...interface{}) *ValidationError {
	if err.err != nil {
		return err
	}
	return err.valid.Expect(cond, msg, items...)
}
