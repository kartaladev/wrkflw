package kernel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// decodeCursorInto parses an opaque base64-of-JSON cursor into dst, which must
// be a non-nil pointer to a struct.
//
// It is the one strict-decoding path shared by every cursor family in this
// package. It returns a BARE error: each family wraps the result in its own
// sentinel ([ErrBadCursor], [ErrBadArmedTimerCursor]) at its own call site, so
// that what a sentinel can mean stays readable in one file instead of being
// split between the family and this helper.
//
// Three guards, each load-bearing:
//
//   - base64 framing, which rejects a cursor that was never one of ours;
//   - DisallowUnknownFields, because unmarshalling into a struct otherwise
//     IGNORES fields it does not recognise — which is exactly what let a
//     foreign cursor through as a zero-ish key instead of an error;
//   - a trailing-data check, because [json.Decoder.Decode] reads only the FIRST
//     JSON value and silently ignores whatever follows. The plain
//     [json.Unmarshal] this supersedes rejects trailing bytes, so without this
//     the "hardened" decoder would be strictly weaker than the code it
//     replaced, and an attacker-supplied cursor could carry a second payload
//     past review. Trailing WHITESPACE is legal JSON framing and stays
//     accepted; only a further value or garbage is rejected.
//
// dst must be a struct pointer. DisallowUnknownFields is silently a no-op for
// map and interface destinations, so a non-struct dst would quietly lose the
// second guard.
func decodeCursorInto(cursor string, dst any) error {
	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}

	// Decode consumed exactly one JSON value; anything left that is not
	// whitespace means the payload carried more than it should.
	//
	// Two distinct causes reach here and both matter to whoever is debugging a
	// rejected cursor: a genuine second JSON value (err == nil), and corrupt
	// trailing bytes (a *json.SyntaxError naming the offending character). Keep
	// the underlying error when there is one rather than collapsing both to the
	// same message.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("trailing data after cursor payload: %w", err)
		}
		return errors.New("trailing data after cursor payload")
	}
	return nil
}

// encodeCursorPayload marshals p and base64-encodes it, returning the
// [json.Marshal] error UNWRAPPED so each caller can wrap it with a message
// naming the entity whose cursor failed.
//
// It exists so no caller can accidentally reintroduce the defect this package
// shipped with: discarding the marshal error and returning the empty string,
// which IS the first-page sentinel.
func encodeCursorPayload(p any) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
