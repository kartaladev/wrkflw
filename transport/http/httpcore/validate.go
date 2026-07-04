package httpcore

import (
	"fmt"

	"github.com/go-playground/validator/v10"
)

// validate is a shared, concurrency-safe validator instance.
var validate = validator.New(validator.WithRequiredStructEnabled())

// Validate runs v's `validate:` struct tags. On failure it returns an error
// wrapping ErrBadInput so ClassifyError maps the response to 400 Bad Request.
// Returns nil when v is valid or when v carries no validate tags.
func Validate(v any) error {
	if err := validate.Struct(v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadInput, err)
	}
	return nil
}
