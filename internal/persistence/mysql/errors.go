package mysql

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// isConcurrencyError reports whether err is (or wraps) a MySQL deadlock
// (error 1213) or lock-wait timeout (error 1205). Both indicate that the
// caller should retry the operation — the engine maps them to
// runtime.ErrConcurrentUpdate.
func isConcurrencyError(err error) bool {
	var me *mysqldriver.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	return me.Number == 1213 || me.Number == 1205
}

// isUniqueViolation reports whether err is (or wraps) a MySQL duplicate-key
// violation (error 1062), used to map a duplicate instance insert to
// runtime.ErrInstanceExists.
func isUniqueViolation(err error) bool {
	var me *mysqldriver.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	return me.Number == 1062
}
