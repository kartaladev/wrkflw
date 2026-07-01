package database_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

func TestFromRejectsUnsupportedConn(t *testing.T) {
	_, err := database.From("not a conn")
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}

func TestBeginTxRejectsUnsupportedConn(t *testing.T) {
	_, err := database.BeginTx(t.Context(), 42)
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}
