package httpcore_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// TestBaseline_ClassifyError_ConcurrentUpdateIsConflict characterizes the 409
// row of openspec/specs/http-transport "Status, code and message rendering per
// error class": a concurrent-update error classifies 409 with the error code
// "conflict", and its text (internal marker included) is rendered.
//
// TestClassifyError_ChainRenderingPerArm already pins the status and the
// rendering for this arm; this test adds the envelope code it never asserts.
func TestBaseline_ClassifyError_ConcurrentUpdateIsConflict(t *testing.T) {
	t.Parallel()

	const marker = "BASELINE-MARKER-409"
	err := fmt.Errorf("%w: %s", kernel.ErrConcurrentUpdate, marker)

	status, body := httpcore.ClassifyError(err)

	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "conflict", body.Error)
	assert.Contains(t, body.Message, marker, "the 409 arm renders the error's text")
}
