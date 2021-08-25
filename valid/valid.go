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

// Fetches a string from the validation context, which must have an input
// registered. If the field is not present, the callback is not run. If
// present, but not a string, an error is recorded. Otherwise, the function is
// called with the string for the user to conduct further validation with.
func (valid *Validation) OptionalString(name string, fn func(s string)) {
	if valid.input == nil {
		panic("Attempted to validate fields without input")
	}
	if o, ok := valid.input[name]; ok {
		s, ok := o.(string)
		valid.
			Expect(ok, "Expected %s to be a string", name).
			WithField(name)
		if ok {
			fn(s)
		}
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
