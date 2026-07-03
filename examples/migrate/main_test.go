package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_SQLiteUpThenStatusThenVersion(t *testing.T) {
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"

	var out bytes.Buffer
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "up"}, &out), "up must exit 0")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "version"}, &out))
	assert.Contains(t, out.String(), "1", "version should report head 1")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "status"}, &out))
	assert.Contains(t, strings.ToLower(out.String()), "applied")
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 2, run([]string{"-dialect=sqlite", "-dsn=file:x?mode=memory", "frobnicate"}, &out))
}
