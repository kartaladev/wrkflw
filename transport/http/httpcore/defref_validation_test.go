package httpcore_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// TestStartInputDefRefWireString asserts the def_ref DTO field (de)serializes as
// the string wire form via the Qualifier marshalers, keeping the wire byte-identical.
func TestStartInputDefRefWireString(t *testing.T) {
	t.Parallel()
	body := []byte(`{"def_ref":"order:3","vars":{}}`)
	var in httpcore.StartInput
	if err := json.Unmarshal(body, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.DefRef != model.Version("order", 3) {
		t.Fatalf("DefRef = %+v", in.DefRef)
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"def_ref":"order:3"`) {
		t.Fatalf("wire not string-form: %s", out)
	}
}

// TestStartInstanceMissingDefRef verifies an empty def_ref (zero Qualifier) is
// rejected with ErrBadInput before the service is called — validate:"required"
// alone does not catch a zero struct.
func TestStartInstanceMissingDefRef(t *testing.T) {
	t.Parallel()
	_, _, err := httpcore.StartInstance(t.Context(), nil, httpcore.StartInput{}, nil)
	if !errors.Is(err, httpcore.ErrBadInput) {
		t.Fatalf("want ErrBadInput for empty def_ref, got %v", err)
	}
}
