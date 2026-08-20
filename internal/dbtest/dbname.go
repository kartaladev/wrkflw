package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// testDBPrefix is the common prefix of every per-test database name created on a
// shared server by [RunTestDatabase], [RunTestMySQL] and [RunTestMySQLDSN].
const testDBPrefix = "wrkflw_test_"

// dbNameCounter numbers the per-test databases this process creates. One counter
// serves both engines: the PostgreSQL and MySQL helpers address different
// servers, so sharing it only ever skips numbers, never collides.
var dbNameCounter atomic.Int64

// processTag identifies THIS test binary process among all the processes that may
// be creating databases on one shared server at the same time.
//
// It has to exist because dbNameCounter is per-PROCESS: `go test ./...` builds one
// binary per package and runs up to GOMAXPROCS of them CONCURRENTLY, and with
// WRKFLW_TEST_POSTGRES_DSN / WRKFLW_TEST_MYSQL_DSN set they all target the same
// server. A counter alone gives every one of them "wrkflw_test_1" — measured, two
// concurrent children both printed wrkflw_test_1..3 — so on PostgreSQL one
// CREATE DATABASE fails ("database \"wrkflw_test_1\" already exists", executed) and
// the loser's DROP ... WITH (FORCE) severs the winner's live connections. On MySQL
// it was worse: allocTestMySQLDB used CREATE DATABASE IF NOT EXISTS, so the second
// process succeeded into the SAME database — migrations twice, rows mixed. That
// statement is now a plain CREATE DATABASE, which errors 1007 instead.
//
// The tag is the PID plus 6 random bytes. The PID separates processes alive at the
// same moment on one host (the case above); the random half separates processes on
// different hosts or in different PID namespaces pointed at the same server, and
// covers PID reuse after a process exits.
var processTag = newProcessTag()

// newProcessTag builds the per-process half of a per-test database name. The
// result is lower-case alphanumeric plus '_', so the name stays a legal UNQUOTED
// identifier on both engines and survives being spliced verbatim into a DSN.
func newProcessTag() string {
	var b [6]byte
	// crypto/rand.Read never returns an error: it fills b or crashes the program.
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("p%d_%s", os.Getpid(), hex.EncodeToString(b[:]))
}

// nextTestDBName returns the name of the next per-test database to create: unique
// within this process (the counter) AND disjoint from every other process's names
// (the tag).
//
// Length budget, worst case — "wrkflw_test_" (12) + "p" + a 7-digit PID (8) + "_"
// + 12 hex characters (13) + "_" + a 19-digit counter (20) = 53 bytes. That fits
// PostgreSQL's 63-byte identifier limit, which TRUNCATES silently, and MySQL's
// 64-character database-name limit, which rejects. Typical names are ~34 bytes.
func nextTestDBName() string {
	return fmt.Sprintf("%s%s_%d", testDBPrefix, processTag, dbNameCounter.Add(1))
}

// ownedTestDBName returns nil when name is one this process created, and an error
// naming it otherwise.
//
// Both cleanup paths route their DROP DATABASE through it. Dropping is the
// destructive half of the shared-server path: a stray DROP does not merely fail
// the dropping test, it deletes the database another package's test binary is
// running against — and PostgreSQL's WITH (FORCE) severs that binary's live
// connections first. Names are process-unique, so this is a second line of
// defence rather than the first; it is what makes "a process can only ever drop
// what it created" a property of the code instead of a property of the naming
// convention holding.
func ownedTestDBName(name string) error {
	if !strings.HasPrefix(name, testDBPrefix+processTag+"_") {
		return fmt.Errorf(
			"workflow-dbtest: refusing to drop database %q: this process (tag %s) did not create it",
			name, processTag)
	}
	return nil
}
