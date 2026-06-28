package mysql_test

import (
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

func TestIsConcurrencyError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"deadlock (1213)":          {err: &mysqldriver.MySQLError{Number: 1213}, want: true},
		"lock wait timeout (1205)": {err: &mysqldriver.MySQLError{Number: 1205}, want: true},
		"duplicate key (1062)":     {err: &mysqldriver.MySQLError{Number: 1062}, want: false},
		"other mysql error (1064)": {err: &mysqldriver.MySQLError{Number: 1064}, want: false},
		"nil":                      {err: nil, want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, mypkg.IsConcurrencyError(tc.err))
		})
	}
}

func TestIsUniqueViolation(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"duplicate key (1062)":     {err: &mysqldriver.MySQLError{Number: 1062}, want: true},
		"deadlock (1213)":          {err: &mysqldriver.MySQLError{Number: 1213}, want: false},
		"lock wait timeout (1205)": {err: &mysqldriver.MySQLError{Number: 1205}, want: false},
		"nil":                      {err: nil, want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, mypkg.IsUniqueViolation(tc.err))
		})
	}
}
