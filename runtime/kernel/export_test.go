// Package kernel exposes internal helpers that are needed only in tests.
// This file is compiled exclusively during test runs (package kernel, not
// kernel_test) so that the black-box tests in cursorcodec_test.go can reach the
// unexported cursor decoder without moving those tests in-package.
package kernel

// DecodeCursorIntoForTest exposes the internal decodeCursorInto helper for use
// by the package-level black-box cursor tests. It MUST NOT be called from
// non-test code.
func DecodeCursorIntoForTest(cursor string, dst any) error {
	return decodeCursorInto(cursor, dst)
}

// InstanceCursorPayloadForTest returns a fresh, empty instance-cursor payload
// as an `any` suitable for passing to DecodeCursorIntoForTest, so black-box
// tests can exercise the decoder against the real payload shape without the
// unexported type escaping the package.
func InstanceCursorPayloadForTest() any {
	return &cursorPayload{}
}
