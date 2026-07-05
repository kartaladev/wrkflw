package httpcore

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// validate is a shared, concurrency-safe validator instance. RegisterTagNameFunc
// makes validation errors reference the JSON wire field name (e.g. "def_ref")
// rather than the Go struct field name (e.g. "DefRef"), so 400 messages describe
// the payload the client actually sent and do not expose internal Go identifiers.
var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return fld.Name
		}
		return name
	})
	return v
}

// Validate runs v's `validate:` struct tags. On failure it returns an error
// wrapping ErrBadInput so ClassifyError maps the response to 400 Bad Request.
// Returns nil when v is valid or when v carries no validate tags.
func Validate(v any) error {
	if err := validate.Struct(v); err != nil {
		return fmt.Errorf("%w: %w", ErrBadInput, err)
	}
	return nil
}
