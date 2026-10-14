#!/usr/bin/env bash
# Assert that OpenSpec reads openspec/config.yaml the way it is written, that
# every change and spec validates strictly, and that no archived change has
# unticked tasks.
#
# WHY THIS EXISTS. OpenSpec degrades a broken config to a warning, never an
# error, and `openspec validate` does not read the config at all. Measured on
# CLI 1.14.1, each of these leaves `openspec validate --all --strict` exiting 0:
#   * invalid YAML — the whole file is ignored ("could not parse … ignoring it");
#   * a misspelled artifact id under rules (`task:`) — that block is ignored;
#   * a misspelled operation id (`aply:`) — its guidance is ignored;
#   * an unquoted ": " inside a rule — YAML reads it as a mapping and every rule
#     for that artifact is ignored;
#   * an unknown top-level key (`rule:`) — ignored with NO warning at all.
# The first four print a warning on stderr of `openspec instructions`, which
# nobody reads in CI. So this script asks for every artifact's instructions and
# fails on any stderr, and checks the top-level keys itself for the fifth.
#
# HOW. The instructions need a change to resolve against, and the repo may have
# none active, so the config is probed in a scratch copy of openspec/ holding a
# throwaway change. Nothing in the working tree is written.
#
# The self-test runs on every invocation: it plants each failure above in a
# scratch copy and requires a non-zero verdict, and requires the unmutated
# config to pass, so a green result also proves the check can still fail.
#
# Usage:
#   scripts/check-openspec.sh              # self-test, then check the repo
#   scripts/check-openspec.sh --self-test  # self-test only
#
# Needs: openspec (the version CONTRIBUTING.md names), bash, awk, mktemp.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ALLOWED_KEYS="schema context rules operations"
INSTRUCTION_IDS="proposal specs design tasks apply"
PROBE_CHANGE="zz-check-openspec-probe"

# Telemetry and the update check are off in CI already; off locally too, so a
# network hiccup never reaches stderr and reads as a config error.
export OPENSPEC_TELEMETRY=0

# check_config <dir holding openspec/>: print each problem, return 1 if any.
check_config() {
  local dir="$1" cfg="$1/openspec/config.yaml" bad=0 key id scratch err
  if [ ! -f "${cfg}" ]; then
    echo "check-openspec: ${cfg} is missing" >&2
    return 1
  fi

  # Top-level keys: an unindented, uncommented `name:` line.
  while IFS= read -r key; do
    case " ${ALLOWED_KEYS} " in
      *" ${key} "*) ;;
      *) echo "check-openspec: unknown top-level key '${key}' in openspec/config.yaml (OpenSpec ignores it silently; allowed: ${ALLOWED_KEYS})" >&2
         bad=1 ;;
    esac
  done < <(awk -F: '/^[A-Za-z_][A-Za-z0-9_-]*:/ { print $1 }' "${cfg}")

  scratch="$(mktemp -d)"
  cp -R "${dir}/openspec" "${scratch}/"
  if ! ( cd "${scratch}" && openspec new change "${PROBE_CHANGE}" >/dev/null 2>&1 ); then
    echo "check-openspec: could not create a probe change in a scratch copy" >&2
    rm -rf "${scratch}"
    return 1
  fi
  for id in ${INSTRUCTION_IDS}; do
    err="$(cd "${scratch}" && openspec instructions "${id}" --change "${PROBE_CHANGE}" --json 2>&1 >/dev/null)" || {
      echo "check-openspec: 'openspec instructions ${id}' failed: ${err}" >&2
      bad=1
      continue
    }
    if [ -n "${err}" ]; then
      echo "check-openspec: 'openspec instructions ${id}' warned, so part of openspec/config.yaml is being ignored:" >&2
      printf '  %s\n' "${err}" >&2
      bad=1
    fi
  done
  rm -rf "${scratch}"
  return "${bad}"
}

# --- self-test ---------------------------------------------------------------

# expect_fail <name> <shell command>: apply the command to a fresh copy of
# openspec/, then require check_config to FAIL on it.
self_test() {
  local status=0 scratch name
  scratch="$(mktemp -d)"

  if ! check_config "${ROOT}" 2>/dev/null; then
    echo "self-test: the unmutated openspec/config.yaml fails the check" >&2
    status=1
  fi

  expect_fail() {
    name="$1"
    rm -rf "${scratch}/openspec"
    cp -R "${ROOT}/openspec" "${scratch}/"
    eval "$2"
    if check_config "${scratch}" 2>/dev/null; then
      echo "self-test: mutation '${name}' passed the check; it must fail" >&2
      status=1
    fi
  }
  local c="${scratch}/openspec/config.yaml"
  expect_fail "invalid YAML"          "printf 'rules: [\n' >> '${c}'"
  expect_fail "unknown top-level key" "printf 'rule:\n  x: 1\n' >> '${c}'"
  expect_fail "misspelled artifact"   "sed -i.bak 's/^  tasks:/  task:/' '${c}'"
  expect_fail "misspelled operation"  "sed -i.bak 's/^  apply:/  aply:/' '${c}'"
  expect_fail "unquoted colon rule"   "awk '!d && /^    - [A-Z]/ { sub(/^    - /, \"    - Note: \"); d=1 } 1' '${c}' > '${c}.new' && mv '${c}.new' '${c}'"

  rm -rf "${scratch}"
  return "${status}"
}

if ! command -v openspec >/dev/null 2>&1; then
  echo "check-openspec: the openspec CLI is not installed (npm install -g @fission-ai/openspec@1.14.1)" >&2
  exit 1
fi

if ! self_test; then
  echo "check-openspec: self-test failed; the check cannot be trusted" >&2
  exit 1
fi
[ "${1:-}" = "--self-test" ] && { echo "check-openspec: self-test ok"; exit 0; }

status=0
check_config "${ROOT}" || status=1
( cd "${ROOT}" && openspec validate --all --strict --no-interactive ) || status=1
# An archived change with unticked tasks merged unfinished.
( cd "${ROOT}" && openspec validate --archived --no-interactive ) || status=1
[ "${status}" = 0 ] && echo "check-openspec: ok"
exit "${status}"
