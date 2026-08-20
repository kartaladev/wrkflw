#!/usr/bin/env bash
# Start / stop the long-lived PostgreSQL and MySQL servers that the test suite can
# share across every package (blocker 7).
#
# WHY
#   internal/dbtest boots its containers from a package-level sync.Once, and Go
#   builds one test binary per package — so "once per binary" is one boot per
#   PACKAGE. A full `go test ./...` pays 10 PostgreSQL boots and 7 MySQL boots —
#   the packages whose own *_test.go files call RunTestDatabase / RunTestMySQL*,
#   re-counted 2026-08-20. Pointing every binary at one already-running server via
#   WRKFLW_TEST_POSTGRES_DSN / WRKFLW_TEST_MYSQL_DSN collapses that to one each.
#
#   Per-test isolation is unchanged either way: every test still gets its own
#   freshly CREATEd database, dropped in t.Cleanup. Only the server's lifetime
#   moves — from "per test binary" to "until you run `down`".
#
# OPT-IN LOCALLY, ALWAYS-ON IN CI. With neither variable exported, the helpers boot
# testcontainers exactly as before, so no local `go test` requires this script —
# but .github/workflows/ci.yml runs it on every build, so CI always takes the
# shared-server path and never exercises the per-package fallback.
#
# USAGE
#   eval "$(scripts/testdb.sh up)"   # start both, export the two DSNs into THIS shell
#   scripts/testdb.sh up             # start both, print the exports for you to copy
#   scripts/testdb.sh env            # print bare KEY=value lines (for $GITHUB_ENV)
#   scripts/testdb.sh status         # show whether each container is running
#   scripts/testdb.sh down           # stop and remove both
#
#   `up` is idempotent: an already-running container is reused, not recreated.
#
# REQUIREMENTS
#   A running Docker daemon. `up` and `down` DO start/stop containers — that is
#   their entire purpose — so run them deliberately.
set -euo pipefail

PG_CONTAINER="${WRKFLW_TESTDB_PG_NAME:-wrkflw-testdb-postgres}"
MY_CONTAINER="${WRKFLW_TESTDB_MY_NAME:-wrkflw-testdb-mysql}"

# Host ports. Non-default so this never collides with a developer's own local
# server on 5432/3306.
PG_PORT="${WRKFLW_TESTDB_PG_PORT:-55432}"
MY_PORT="${WRKFLW_TESTDB_MY_PORT:-53306}"

# Credentials and image tags mirror internal/dbtest exactly, so the shared-server
# path and the container path exercise the same server versions.
PG_IMAGE="postgres:17-alpine"
MY_IMAGE="mysql:8.0"
PG_USER="wrkflw"
PG_PASSWORD="wrkflw"
PG_DB="wrkflw_test"
MY_ROOT_PASSWORD="wrkflw_root"
MY_DB="wrkflw_test"

# max_connections. One shared server now fronts EVERY package's pools at once
# rather than one package's, so the 300 inherited from dbtest's serverMaxConns had
# to be re-derived rather than assumed. It holds:
#
#   Worst case on a 4-vCPU CI runner: `go test` runs -p=GOMAXPROCS package binaries
#   at once and -parallel=GOMAXPROCS tests inside each, and each test's pool caps at
#   perTestMaxConns=8 (internal/dbtest/postgres.go), plus one 4-connection admin
#   pool per binary — 4 x 4 x 8 + 4 x 4 = 144 connection SLOTS, under 300.
#
#   Measured 2026-08-20 (14-core dev machine, `go test -race` over 6 package trees
#   incl. runtime, scheduler, internal/database, casbinauthz): peak 8 concurrent
#   client backends, 2 concurrent per-test databases. pgxpool's MaxConns is a lazy
#   cap, not a preallocation, so real demand sits far below the arithmetic bound.
#
# Raise it only against a measurement: a bigger number costs shared memory for
# slots the lazy pools never open.
PG_MAX_CONNECTIONS=300
MY_MAX_CONNECTIONS=500

usage() {
  # 2,33 is the whole header comment block, ending at the last REQUIREMENTS line
  # (line 34 is `set -euo pipefail`). Keep this range in step with edits above.
  sed -n '2,33p' "$0" | sed -e 's/^# \{0,1\}//'
  exit "${1:-0}"
}

need_docker() {
  command -v docker >/dev/null 2>&1 || {
    echo "testdb.sh: docker not found on PATH" >&2
    exit 1
  }
  docker info >/dev/null 2>&1 || {
    echo "testdb.sh: the Docker daemon is not reachable — start it and retry" >&2
    exit 1
  }
}

container_state() {
  # Prints "running", "exited", ... or "" when no such container exists.
  docker inspect -f '{{.State.Status}}' "$1" 2>/dev/null || true
}

pg_dsn() {
  echo "postgres://${PG_USER}:${PG_PASSWORD}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable"
}

my_dsn() {
  echo "root:${MY_ROOT_PASSWORD}@tcp(127.0.0.1:${MY_PORT})/${MY_DB}?parseTime=true&loc=UTC&multiStatements=true"
}

start_postgres() {
  local state
  state="$(container_state "${PG_CONTAINER}")"
  case "${state}" in
    running) echo "testdb.sh: ${PG_CONTAINER} already running" >&2; return ;;
    "")      ;;
    *)       echo "testdb.sh: restarting ${PG_CONTAINER} (was ${state})" >&2
             docker start "${PG_CONTAINER}" >/dev/null
             return ;;
  esac

  echo "testdb.sh: starting ${PG_CONTAINER} (${PG_IMAGE}) on 127.0.0.1:${PG_PORT}" >&2
  docker run -d --name "${PG_CONTAINER}" \
    -e POSTGRES_USER="${PG_USER}" \
    -e POSTGRES_PASSWORD="${PG_PASSWORD}" \
    -e POSTGRES_DB="${PG_DB}" \
    -p "127.0.0.1:${PG_PORT}:5432" \
    "${PG_IMAGE}" \
    postgres -c "max_connections=${PG_MAX_CONNECTIONS}" >/dev/null
}

start_mysql() {
  local state
  state="$(container_state "${MY_CONTAINER}")"
  case "${state}" in
    running) echo "testdb.sh: ${MY_CONTAINER} already running" >&2; return ;;
    "")      ;;
    *)       echo "testdb.sh: restarting ${MY_CONTAINER} (was ${state})" >&2
             docker start "${MY_CONTAINER}" >/dev/null
             return ;;
  esac

  echo "testdb.sh: starting ${MY_CONTAINER} (${MY_IMAGE}) on 127.0.0.1:${MY_PORT}" >&2
  docker run -d --name "${MY_CONTAINER}" \
    -e MYSQL_ROOT_PASSWORD="${MY_ROOT_PASSWORD}" \
    -e MYSQL_DATABASE="${MY_DB}" \
    -p "127.0.0.1:${MY_PORT}:3306" \
    "${MY_IMAGE}" \
    --max-connections="${MY_MAX_CONNECTIONS}" >/dev/null
}

wait_postgres() {
  local i
  for i in $(seq 1 60); do
    if docker exec "${PG_CONTAINER}" pg_isready -U "${PG_USER}" -d "${PG_DB}" >/dev/null 2>&1; then
      echo "testdb.sh: postgres ready after ${i}s" >&2
      return 0
    fi
    sleep 1
  done
  echo "testdb.sh: postgres did not become ready within 60s; check 'docker logs ${PG_CONTAINER}'" >&2
  return 1
}

wait_mysql() {
  local i
  for i in $(seq 1 120); do
    if docker exec "${MY_CONTAINER}" mysqladmin ping -uroot -p"${MY_ROOT_PASSWORD}" --silent >/dev/null 2>&1; then
      echo "testdb.sh: mysql ready after ${i}s" >&2
      return 0
    fi
    sleep 1
  done
  echo "testdb.sh: mysql did not become ready within 120s; check 'docker logs ${MY_CONTAINER}'" >&2
  return 1
}

cmd_up() {
  need_docker
  start_postgres
  start_mysql
  wait_postgres
  wait_mysql

  # stdout carries ONLY the exports, so `eval "$(scripts/testdb.sh up)"` works.
  # Every progress message above went to stderr for exactly this reason.
  echo "export WRKFLW_TEST_POSTGRES_DSN='$(pg_dsn)'"
  echo "export WRKFLW_TEST_MYSQL_DSN='$(my_dsn)'"
}

cmd_down() {
  need_docker
  local c
  for c in "${PG_CONTAINER}" "${MY_CONTAINER}"; do
    if [[ -n "$(container_state "${c}")" ]]; then
      echo "testdb.sh: removing ${c}" >&2
      docker rm -f "${c}" >/dev/null
    else
      echo "testdb.sh: ${c} not present" >&2
    fi
  done
  echo "unset WRKFLW_TEST_POSTGRES_DSN"
  echo "unset WRKFLW_TEST_MYSQL_DSN"
}

# cmd_env prints the same two DSNs as `up`, but as bare KEY=value lines with no
# `export` and no quoting — the format GitHub Actions' $GITHUB_ENV file expects.
# It starts nothing, so `up` must have run first.
cmd_env() {
  echo "WRKFLW_TEST_POSTGRES_DSN=$(pg_dsn)"
  echo "WRKFLW_TEST_MYSQL_DSN=$(my_dsn)"
}

cmd_status() {
  need_docker
  local c state
  for c in "${PG_CONTAINER}" "${MY_CONTAINER}"; do
    state="$(container_state "${c}")"
    printf '%-28s %s\n' "${c}" "${state:-absent}"
  done
}

case "${1:-}" in
  up)     cmd_up ;;
  down)   cmd_down ;;
  env)    cmd_env ;;
  status) cmd_status ;;
  -h|--help|help|"") usage 0 ;;
  *) echo "testdb.sh: unknown command '$1'" >&2; usage 1 ;;
esac
