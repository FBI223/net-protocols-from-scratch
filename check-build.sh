#!/usr/bin/env bash
#
# check-build.sh - checks dependencies and builds of the Go / Rust / C++ projects in a repo
# and reports how many tests really ran (so skipped or empty tests do not look like success).
# Needs the toolchains (go, cargo, cmake + a compiler); missing ones are skipped.
# The repo is left untouched: the C++ build goes to a temp directory, Rust's target/ goes to
# ~/.cache/repo-checks/ (override with CARGO_TARGET_DIR), and go.mod/go.sum are restored after
# the 'go mod tidy' check (Go >= 1.23 uses 'go mod tidy -diff').
#
# Usage:
#   ./check-build.sh [options] [path-to-repo]     (default: current directory)
#
# Options:
#   --only go,rust,cpp   check only the selected languages
#   --skip-tests         do not run tests (network tests may need ports/privileges)
#   --clippy             also run cargo clippy -D warnings
#   --vuln               also run govulncheck / cargo audit (if installed)
#   --write-locks        let cargo leave newly generated Cargo.lock files in the repo (default: removed)
#   --timeout SEC        time limit of a single step (default 600)
#   --jobs N             C++ build parallelism (default: number of cores)
#   -v, --verbose        show the full output of every step
#   --no-color           disable colors
#   -h, --help           this help
#
# Exit code: 0 = all good, 1 = errors, 2 = bad argument.
#
# What it checks:
#   Go    : go.mod version vs local, go mod tidy (go.mod/go.sum up to date), go mod verify,
#           go build ./..., go vet ./..., gofmt, go test ./... (passed / skipped / failed counts,
#           warns about skipped placeholder tests)
#   Rust  : cargo metadata (every crate belongs to a workspace, path= dependencies inside the repo),
#           cargo fmt --check, cargo check --locked (up-to-date Cargo.lock), cargo test
#           (passed / ignored counts, warns when zero tests ran)
#   C++   : cmake configure + build of every root CMakeLists.txt (with -Wall -Wextra),
#           compiler warning count, ctest (warns when there are no tests)

set -uo pipefail

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

ONLY="go,rust,cpp"
SKIP_TESTS=0
CLIPPY=0
VULN=0
WRITE_LOCKS=0
TIMEOUT=600
JOBS=""
VERBOSE=0
USE_COLOR=1
[[ -t 1 ]] || USE_COLOR=0
[[ -n "${NO_COLOR:-}" ]] && USE_COLOR=0
ROOT_ARG="."

need_arg() { [[ $# -ge 2 ]] || { echo "$1 needs an argument" >&2; exit 2; }; }
while (($#)); do
  case "$1" in
    --only) need_arg "$@"; ONLY="$2"; shift ;;
    --skip-tests) SKIP_TESTS=1 ;;
    --clippy) CLIPPY=1 ;;
    --vuln) VULN=1 ;;
    --write-locks) WRITE_LOCKS=1 ;;
    --timeout) need_arg "$@"; TIMEOUT="$2"; shift ;;
    --jobs) need_arg "$@"; JOBS="$2"; shift ;;
    -v|--verbose) VERBOSE=1 ;;
    --no-color) USE_COLOR=0 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "Unknown option: $1 (use --help)" >&2; exit 2 ;;
    *) ROOT_ARG="$1" ;;
  esac
  shift
done

[[ "$TIMEOUT" =~ ^[0-9]+$ ]] || { echo "--timeout needs a number of seconds" >&2; exit 2; }
RESOLVED=$(cd "$ROOT_ARG" 2>/dev/null && pwd -P) || { echo "No such directory: $ROOT_ARG" >&2; exit 2; }
ROOT="$RESOLVED"
ROOT_ESC=$(printf '%s' "$ROOT" | sed 's/[][\.*^$#/]/\\&/g')
IS_TTY=0; [[ -t 1 ]] && IS_TTY=1
# step logs are captured, so we do not want tool colors
export CARGO_TERM_COLOR=never NO_COLOR=1
# Rust's target/ goes outside the repo (the repo stays clean) but into a persistent cache so later runs are fast
if [[ -z "${CARGO_TARGET_DIR:-}" ]]; then
  CACHE_KEY=$(printf '%s' "$ROOT" | cksum | cut -d' ' -f1)
  export CARGO_TARGET_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/repo-checks/cargo-target-$CACHE_KEY"
fi

wants() { [[ ",$ONLY," == *",$1,"* ]]; }
have() { command -v "$1" >/dev/null 2>&1; }

if [[ -z "$JOBS" ]]; then
  JOBS=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)
fi
TIMEOUT_CMD=""
if have timeout; then TIMEOUT_CMD="timeout"; elif have gtimeout; then TIMEOUT_CMD="gtimeout"; fi

if ((USE_COLOR)); then
  C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'; C_BLU=$'\033[36m'
  C_BLD=$'\033[1m'; C_RST=$'\033[0m'
else
  C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''; C_RST=''
fi

N_OK=0; N_WARN=0; N_FAIL=0; N_SKIP=0
GO_PASS=0; GO_SKIPPED=0; GO_FAILED=0; RS_PASS=0; RS_IGNORED=0; RS_FAILED=0; TESTS_RAN=0
FAILED=()
CTX="repo"
TAIL_FILTER=""
ok()   { N_OK=$((N_OK + 1));     printf '  %s[ OK ]%s %s\n' "$C_GRN" "$C_RST" "$*"; }
warn() { N_WARN=$((N_WARN + 1)); printf '  %s[WARN]%s %s\n' "$C_YEL" "$C_RST" "$*"; }
fail() { N_FAIL=$((N_FAIL + 1)); printf '  %s[FAIL]%s %s\n' "$C_RED" "$C_RST" "$*"; }
skip() { N_SKIP=$((N_SKIP + 1)); printf '  %s[skip]%s %s\n' "$C_BLU" "$C_RST" "$*"; }
info() { printf '  %s[info]%s %s\n' "$C_BLU" "$C_RST" "$*"; }
detail() { printf '%s\n' "$*" | fold -s -w 100 | sed 's/^/           /'; }
section() { printf '\n%s== %s ==%s\n' "$C_BLD" "$*" "$C_RST"; }
subsection() { printf '\n  %s-- %s%s\n' "$C_BLD" "$*" "$C_RST"; }

TMPD=$(mktemp -d) || exit 2
RESTORE_DIR=""; RESTORE_BK=""
CREATED_LOCKS=()
restore_go() {
  [[ -n "$RESTORE_DIR" && -d "$RESTORE_BK" ]] || return 0
  cp "$RESTORE_BK/go.mod" "$RESTORE_DIR/go.mod"
  if [[ -f "$RESTORE_BK/go.sum" ]]; then cp "$RESTORE_BK/go.sum" "$RESTORE_DIR/go.sum"; else rm -f "$RESTORE_DIR/go.sum"; fi
  RESTORE_DIR=""
}
# shellcheck disable=SC2329  # invoked by the trap
cleanup() {
  restore_go
  local l
  for l in ${CREATED_LOCKS[@]+"${CREATED_LOCKS[@]}"}; do rm -f "$l"; done
  rm -rf "$TMPD"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

LOGN=0
LAST_LOG=""

net_hint() {
  grep -qEi 'could not resolve|failed to download|timed out|dial tcp|no such host|network|Temporary failure|proxy' "$LAST_LOG" 2>/dev/null &&
    detail "looks like a network problem: the first build downloads dependencies (check your connection / proxy)"
  return 0
}

# run_step LABEL DIR CMD...  -> 0 when ok; on failure prints the end of the log
run_step() {
  local label="$1" dir="$2"; shift 2
  LOGN=$((LOGN + 1)); LAST_LOG="$TMPD/log.$LOGN"
  local t0=$SECONDS rc=0
  ((IS_TTY)) && printf '  %s[ .. ]%s %s\r' "$C_BLU" "$C_RST" "$label"
  if [[ -n "$TIMEOUT_CMD" ]]; then
    (cd "$dir" && "$TIMEOUT_CMD" "$TIMEOUT" "$@") >"$LAST_LOG" 2>&1 || rc=$?
  else
    (cd "$dir" && "$@") >"$LAST_LOG" 2>&1 || rc=$?
  fi
  ((IS_TTY)) && printf '\033[2K\r'
  local dt=$((SECONDS - t0))
  if ((rc == 0)); then
    ok "$label (${dt}s)"
    ((VERBOSE)) && sed 's/^/           | /' "$LAST_LOG"
    return 0
  fi
  if ((rc == 124)); then fail "$label: exceeded the ${TIMEOUT}s limit"; else fail "$label (exit code $rc, ${dt}s)"; fi
  FAILED+=("$CTX: $label")
  if [[ -n "$TAIL_FILTER" ]] && grep -qE -- "$TAIL_FILTER" "$LAST_LOG"; then
    grep -E -A3 -- "$TAIL_FILTER" "$LAST_LOG" | head -n 40 | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
  else
    tail -n 25 "$LAST_LOG" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
  fi
  net_hint
  return "$rc"
}

find_manifests() { # name -> absolute paths, shallower first
  find "$ROOT" \( -name .git -o -name target -o -name node_modules -o -name vendor -o -name testdata \
      -o -name build -o -name 'cmake-build-*' \) -prune -o -type f -name "$1" -print \
    | awk '{ n = gsub("/", "/"); printf "%d\t%s\n", n, $0 }' | LC_ALL=C sort -t "$(printf '\t')" -k1,1n -k2,2 | cut -f2-
}
has_bin() { # does the crate/workspace directory contain a binary?
  [[ -f "$1/src/main.rs" ]] || grep -qE '^\[\[bin\]\]' "$1/Cargo.toml" 2>/dev/null \
    || find "$1" -maxdepth 3 -path '*/src/main.rs' -not -path '*/target/*' 2>/dev/null | grep -q .
}
rel_of() { local r="${1#"$ROOT"}"; r="${r#/}"; printf '%s' "${r:-.}"; }
inside_root() { [[ "$1" == "$ROOT" || "$1" == "$ROOT"/* ]]; }

# --- test summaries (read LAST_LOG of the test step) -------------------------
summarize_go_tests() { # rc of the test step
  local rc="$1" p f s ph reasons l
  p=$(grep -c '^--- PASS' "$LAST_LOG" || true)
  f=$(grep -c '^--- FAIL' "$LAST_LOG" || true)
  s=$(grep -c '^--- SKIP' "$LAST_LOG" || true)
  ph=$(grep -c '^--- SKIP: TestPlaceholder' "$LAST_LOG" || true)
  GO_PASS=$((GO_PASS + p)); GO_SKIPPED=$((GO_SKIPPED + s)); GO_FAILED=$((GO_FAILED + f)); TESTS_RAN=1
  info "Go tests: $p passed, $s skipped, $f failed (top-level tests and fuzz seed corpora)"
  if ((rc == 0 && p == 0)); then
    warn "no Go test passed: the test files are empty or every test is skipped"
  fi
  if ((ph > 0)); then
    warn "$ph placeholder tests (TestPlaceholder) are skipped: replace them with real tests"
  fi
  if ((s > ph)); then
    reasons=$(grep -B1 '^--- SKIP' "$LAST_LOG" | grep -E '^ +[^ ]+:[0-9]+: ' | sed -E 's/^ +[^ :]+:[0-9]+: //' | sort | uniq -c | sort -rn | head -n 5)
    if [[ -n "$reasons" ]]; then
      printf '           skip reasons:\n'
      while IFS= read -r l; do printf '             %s\n' "$l"; done <<<"$reasons"
    fi
  fi
  return 0
}

summarize_rust_tests() { # rc of the test step
  local rc="$1" p f i tg
  read -r p f i tg _ < <(awk '
    /^test result:/ { p += $4; f += $6; i += $8; tg++; if ($4 + $8 == 0) zero++ }
    END { printf "%d %d %d %d %d\n", p + 0, f + 0, i + 0, tg + 0, zero + 0 }' "$LAST_LOG")
  RS_PASS=$((RS_PASS + p)); RS_IGNORED=$((RS_IGNORED + i)); RS_FAILED=$((RS_FAILED + f)); TESTS_RAN=1
  info "Rust tests: $p passed, $f failed, $i ignored (in $tg test targets)"
  if ((rc == 0 && p == 0)); then
    warn "0 Rust tests ran: all $tg test targets are empty (add #[test] functions; an empty tests/*.rs file always 'passes')"
  fi
  return 0
}

printf '%sRepo:%s %s\n' "$C_BLD" "$C_RST" "$ROOT"
printf 'Tools: go=%s cargo=%s cmake=%s  (jobs=%s, timeout=%ss%s)\n' \
  "$(have go && echo yes || echo no)" "$(have cargo && echo yes || echo no)" "$(have cmake && echo yes || echo no)" \
  "$JOBS" "$TIMEOUT" "$( ((SKIP_TESTS)) && echo ', tests skipped' || true)"

# ==========================================================================
# GO
# ==========================================================================
if wants go; then
  section "Go"
  readarray -t GOMODS < <(find_manifests go.mod)
  if ((${#GOMODS[@]} == 0)); then
    info "no go.mod in the repo"
  elif ! have go; then
    skip "go is not installed: skipping ${#GOMODS[@]} modules"
  else
    GO_LOCAL=$(go env GOVERSION 2>/dev/null); GO_LOCAL="${GO_LOCAL#go}"
    HAS_TIDY_DIFF=0
    go help mod tidy 2>&1 | grep -q -- '-diff' && HAS_TIDY_DIFF=1
    [[ "${CHECK_BUILD_FORCE_TIDY_FALLBACK:-0}" == 1 ]] && HAS_TIDY_DIFF=0 # for testing the script only
    for gm in "${GOMODS[@]}"; do
      dir=$(dirname "$gm"); rel=$(rel_of "$dir")
      mpath=$(awk '/^module[ \t]/ { gsub(/"/, "", $2); print $2; exit }' "$gm")
      CTX="go:$rel"
      subsection "module ${mpath:-?} (${rel})"

      need=$(awk '/^go[ \t]+[0-9]/ { print $2; exit }' "$gm")
      if [[ -n "$need" && -n "$GO_LOCAL" && "$(printf '%s\n%s\n' "$need" "$GO_LOCAL" | sort -V | head -n1)" != "$need" ]]; then
        warn "go.mod needs go $need, local Go is $GO_LOCAL (Go may download a newer toolchain or the build may fail)"
      fi

      # local replace directives leaving the repo
      while IFS= read -r line; do
        tgt=$(sed -E 's/.*=>[[:space:]]*([^[:space:]]+).*/\1/' <<<"$line")
        abs=$(realpath -m "$dir/$tgt" 2>/dev/null || echo "$dir/$tgt")
        if ! inside_root "$abs"; then fail "replace => $tgt points outside the repo (it will break once the repo is split)"; FAILED+=("$CTX: replace outside the repo")
        elif [[ ! -d "$abs" ]]; then fail "replace => $tgt: directory does not exist"; FAILED+=("$CTX: replace target missing"); fi
      done < <(grep -E '=>[[:space:]]*\.{1,2}/' "$gm" || true)

      # go mod tidy: are go.mod/go.sum up to date
      if ((HAS_TIDY_DIFF)); then
        run_step "go mod tidy -diff (go.mod/go.sum up to date)" "$dir" go mod tidy -diff \
          || detail "fix: (cd $rel && go mod tidy)"
      else
        RESTORE_BK="$TMPD/tidy.$((LOGN + 1))"; mkdir -p "$RESTORE_BK"
        cp "$dir/go.mod" "$RESTORE_BK/go.mod"; [[ -f "$dir/go.sum" ]] && cp "$dir/go.sum" "$RESTORE_BK/go.sum"
        RESTORE_DIR="$dir"
        LAST_LOG="$TMPD/tidy.log"
        trc=0
        (cd "$dir" && go mod tidy) >"$LAST_LOG" 2>&1 || trc=$?
        changed=0
        cmp -s "$dir/go.mod" "$RESTORE_BK/go.mod" || changed=1
        if [[ -f "$RESTORE_BK/go.sum" ]]; then cmp -s "$dir/go.sum" "$RESTORE_BK/go.sum" || changed=1
        elif [[ -f "$dir/go.sum" ]]; then changed=1; fi
        restore_go
        if ((trc != 0)); then
          fail "go mod tidy failed (exit code $trc)"; FAILED+=("$CTX: go mod tidy")
          tail -n 15 "$LAST_LOG" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
          net_hint
        elif ((changed)); then
          fail "go.mod/go.sum are not tidy (changes were reverted)"; FAILED+=("$CTX: go mod tidy")
          detail "fix: (cd $rel && go mod tidy)"
        else
          ok "go mod tidy: go.mod/go.sum up to date"
        fi
      fi

      run_step "go mod verify" "$dir" go mod verify
      run_step "go build ./..." "$dir" go build ./... \
        || detail "typical after splitting a repo: 'use of internal package ... not allowed' or an import of the old module path"
      run_step "go vet ./..." "$dir" go vet ./...

      unformatted=$(cd "$dir" && gofmt -l . 2>&1 | grep -v '^$' || true)
      if [[ -n "$unformatted" ]]; then
        readarray -t _uf <<<"$unformatted"
        warn "${#_uf[@]} files are not gofmt-ed (fix: (cd $rel && gofmt -w .))"
        detail "$(printf '%s, ' "${_uf[@]:0:15}" | sed 's/, $//')"
      else
        ok "gofmt: no issues"
      fi

      if ((SKIP_TESTS)); then
        skip "go test (--skip-tests)"
      else
        TAIL_FILTER='^[[:space:]]*--- FAIL|^FAIL|^panic:'
        trc=0
        run_step "go test ./..." "$dir" go test ./... -count=1 -timeout 120s -v || trc=$?
        TAIL_FILTER=""
        ((trc != 0)) && detail "if these are network tests: they may need free ports/privileges (try --skip-tests)"
        summarize_go_tests "$trc"
      fi

      if ((VULN)); then
        if have govulncheck; then run_step "govulncheck ./..." "$dir" govulncheck ./...
        else skip "govulncheck is not installed (go install golang.org/x/vuln/cmd/govulncheck@latest)"; fi
      fi
    done
  fi
fi

# ==========================================================================
# RUST
# ==========================================================================
if wants rust; then
  section "Rust"
  readarray -t CARGOS < <(find_manifests Cargo.toml)
  if ((${#CARGOS[@]} == 0)); then
    info "no Cargo.toml in the repo"
  elif ! have cargo; then
    skip "cargo is not installed: skipping ${#CARGOS[@]} manifests"
  else
    declare -A COVERED=() WSDONE=() SEEN_ERR=()
    if ((!WRITE_LOCKS)); then
      for cm in "${CARGOS[@]}"; do
        [[ -f "$(dirname "$cm")/Cargo.lock" ]] || CREATED_LOCKS+=("$(dirname "$cm")/Cargo.lock")
      done
    fi
    WS_ROOTS=()
    for cm in "${CARGOS[@]}"; do
      [[ -n "${COVERED[$cm]:-}" ]] && continue
      rel=$(rel_of "$(dirname "$cm")")
      CTX="rust:$rel"
      meta="$TMPD/meta.$((++LOGN))"
      if ! (cd "$(dirname "$cm")" && cargo metadata --no-deps --format-version 1 --manifest-path "$cm") >"$meta" 2>"$meta.err"; then
        subsection "crate $rel"
        errsum=$(cksum < "$meta.err" | cut -d' ' -f1)
        if [[ -n "${SEEN_ERR[$errsum]:-}" ]]; then
          skip "same workspace error as for ${SEEN_ERR[$errsum]} (not repeated)"
          continue
        fi
        SEEN_ERR["$errsum"]="$rel"
        fail "cargo metadata failed"; FAILED+=("$CTX: cargo metadata")
        head -n 8 "$meta.err" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
        if grep -q "believes it's in a workspace when it's not" "$meta.err"; then
          detail "this crate lives under a workspace directory but is not listed in its 'members': add it to members (or exclude), or give it its own empty [workspace] in Cargo.toml"
        elif grep -q 'failed to load manifest for dependency\|failed to read .*Cargo.toml' "$meta.err"; then
          detail "a path=... dependency points to a missing directory (often outside the repo after a split): fix the path, move the crate into the repo, or use a crates.io / git version"
        fi
        continue
      fi
      while IFS= read -r mp; do COVERED["$mp"]=1; done \
        < <(grep -o '"manifest_path":"[^"]*"' "$meta" | sed 's/"manifest_path":"//; s/"$//')
      wsroot=$(grep -o '"workspace_root":"[^"]*"' "$meta" | head -n1 | sed 's/"workspace_root":"//; s/"$//')
      [[ -z "$wsroot" ]] && wsroot=$(dirname "$cm")

      # path=... dependencies leaving the repo
      while IFS= read -r dp; do
        [[ -z "$dp" ]] && continue
        if ! inside_root "$dp"; then
          subsection "crate $rel"
          fail "path dependency points outside the repo: $dp (it will break once the repo is split)"; FAILED+=("$CTX: path dependency outside the repo")
        elif [[ ! -e "$dp" ]]; then
          subsection "crate $rel"
          fail "path dependency does not exist: $dp"; FAILED+=("$CTX: path dependency missing")
        fi
      done < <(grep -o '"path":"[^"]*"' "$meta" | sed 's/"path":"//; s/"$//' | sort -u)

      if [[ -z "${WSDONE[$wsroot]:-}" ]]; then WSDONE["$wsroot"]=1; WS_ROOTS+=("$wsroot"); fi
    done

    if ((${#WS_ROOTS[@]} >= 3)); then
      has_root=0; for ws in "${WS_ROOTS[@]}"; do [[ "$ws" == "$ROOT" ]] && has_root=1; done
      ((has_root)) || info "${#WS_ROOTS[@]} separate crates without a shared workspace: consider a root Cargo.toml with [workspace] members=[...] (one Cargo.lock, one target, one cargo test)"
    fi
    for ws in ${WS_ROOTS[@]+"${WS_ROOTS[@]}"}; do
      rel=$(rel_of "$ws")
      CTX="rust:$rel"
      subsection "workspace/crate ${rel}"
      lock=()
      if [[ -f "$ws/Cargo.lock" ]]; then lock=(--locked)
      elif has_bin "$ws"; then
        warn "no Cargo.lock in $rel (an application/binary: commit it; generate with: cargo generate-lockfile)"
      else
        info "no Cargo.lock in $rel (fine for a pure library; --locked skipped)"
      fi

      if (cd "$ws" && cargo fmt --version >/dev/null 2>&1); then
        run_step "cargo fmt --check" "$ws" cargo fmt --all -- --check || detail "fix: (cd $rel && cargo fmt --all)"
      else
        skip "rustfmt is not installed (rustup component add rustfmt)"
      fi
      run_step "cargo check --workspace --all-targets${lock[*]:+ --locked}" "$ws" \
        cargo check --workspace --all-targets ${lock[@]+"${lock[@]}"} \
        || { grep -q 'lock file.*needs to be updated' "$LAST_LOG" && detail "Cargo.lock is out of date: run cargo update -w, or cargo check without --locked, and commit Cargo.lock"; }
      if ((CLIPPY)); then
        if (cd "$ws" && cargo clippy --version >/dev/null 2>&1); then
          run_step "cargo clippy -D warnings" "$ws" cargo clippy --workspace --all-targets ${lock[@]+"${lock[@]}"} -- -D warnings
        else skip "clippy is not installed (rustup component add clippy)"; fi
      fi
      if ((SKIP_TESTS)); then
        skip "cargo test (--skip-tests)"
      else
        trc=0
        run_step "cargo test --workspace" "$ws" cargo test --workspace ${lock[@]+"${lock[@]}"} || trc=$?
        if ((trc == 0)) || grep -q '^test result:' "$LAST_LOG"; then summarize_rust_tests "$trc"; fi
      fi
      if ((VULN)); then
        if (cd "$ws" && cargo audit --version >/dev/null 2>&1); then run_step "cargo audit" "$ws" cargo audit
        else skip "cargo-audit is not installed (cargo install cargo-audit)"; fi
      fi
    done
  fi
fi

# ==========================================================================
# C++ / CMake
# ==========================================================================
if wants cpp; then
  section "C++ (CMake)"
  readarray -t CMAKES < <(find_manifests CMakeLists.txt)
  if ((${#CMAKES[@]} == 0)); then
    info "no CMakeLists.txt in the repo"
  elif ! have cmake; then
    skip "cmake is not installed: skipping ${#CMAKES[@]} CMakeLists.txt files"
  else
    CXX_FOUND=""
    for c in "${CXX:-}" c++ g++ clang++; do
      [[ -n "$c" ]] && have "$c" && { CXX_FOUND="$c"; break; }
    done
    GEN=()
    have ninja && GEN=(-G Ninja)
    if [[ -z "$CXX_FOUND" ]]; then
      skip "no C++ compiler found (c++/g++/clang++)"
    else
      CM_ROOTS=()
      for cm in "${CMAKES[@]}"; do
        d=$(dirname "$cm"); p=$(dirname "$d"); isroot=1
        while inside_root "$p"; do
          [[ -f "$p/CMakeLists.txt" ]] && { isroot=0; break; }
          [[ "$p" == "$ROOT" ]] && break
          p=$(dirname "$p")
        done
        ((isroot)) && CM_ROOTS+=("$d")
      done
      info "root CMake projects: ${#CM_ROOTS[@]} (compiler: $CXX_FOUND, generator: ${GEN[1]:-default})"
      for r in "${CM_ROOTS[@]}"; do
        rel=$(rel_of "$r")
        CTX="cmake:$rel"
        subsection "project $rel"
        B="$TMPD/cmake.$((++LOGN))"
        if run_step "cmake configure" "$r" cmake -S "$r" -B "$B" -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_CXX_FLAGS="-Wall -Wextra" ${GEN[@]+"${GEN[@]}"}; then
          if run_step "cmake --build (-j$JOBS)" "$r" cmake --build "$B" -j "$JOBS"; then
            nwarn=$(grep -c 'warning:' "$LAST_LOG" || true)
            if ((nwarn > 0)); then
              warn "$nwarn compiler warnings (-Wall -Wextra); first ones:"
              grep -m5 'warning:' "$LAST_LOG" | sed -E "s#$ROOT/##" | cut -c1-160 | sed 's/^/           | /'
            else
              ok "build without warnings (-Wall -Wextra)"
            fi
            if ((SKIP_TESTS)); then
              skip "ctest (--skip-tests)"
            elif have ctest; then
              if run_step "ctest" "$B" ctest --output-on-failure --timeout 120; then
                if grep -q 'No tests were found' "$LAST_LOG"; then
                  warn "ctest found no tests (add enable_testing() and add_test() to CMakeLists.txt)"
                else
                  nct=$(grep -oE 'out of [0-9]+' "$LAST_LOG" | grep -oE '[0-9]+' | tail -n1 || true)
                  [[ -n "$nct" ]] && info "ctest: $nct tests"
                fi
              fi
            else
              skip "ctest is not installed"
            fi
          fi
        fi
      done
    fi
  fi
fi

# ==========================================================================
printf '\n%sSummary:%s %s%d OK%s, %s%d warnings%s, %s%d errors%s, %d skipped\n' "$C_BLD" "$C_RST" \
  "$C_GRN" "$N_OK" "$C_RST" "$C_YEL" "$N_WARN" "$C_RST" "$C_RED" "$N_FAIL" "$C_RST" "$N_SKIP"
if ((TESTS_RAN)); then
  printf '%sTests:%s Go %d passed / %d skipped / %d failed;  Rust %d passed / %d ignored / %d failed\n' "$C_BLD" "$C_RST" \
    "$GO_PASS" "$GO_SKIPPED" "$GO_FAILED" "$RS_PASS" "$RS_IGNORED" "$RS_FAILED"
fi
if ((${#FAILED[@]})); then
  printf '%sTo fix:%s\n' "$C_BLD" "$C_RST"
  printf '  - %s\n' "${FAILED[@]}"
fi
((N_FAIL > 0)) && exit 1
exit 0
