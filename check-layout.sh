#!/usr/bin/env bash
#
# check-layout.sh - static check of a protocol repository: layout, docs, tests, hygiene,
# manifests, plus a per-protocol progress table computed from the code.
# Needs no Go / Rust / CMake and modifies nothing.
#
# Usage:
#   ./check-layout.sh [options] [path-to-repo]     (default: current directory)
#
# Options:
#   --strict          warnings also give exit code 1
#   --no-color        disable colors
#   --ignore a,b,c    extra top-level directories that are NOT protocols
#   --stale REGEX     look for leftovers of an old name/path (may be repeated),
#                     e.g. --stale 'old-repo/go'
#   --min-lines N     fewer than N lines of code = skeleton (status "planned"), default 40
#   --markdown        print only the progress table as GitHub markdown (for CI job summaries)
#   -h, --help        this help
#
# Exit code: 0 = no errors, 1 = errors (or warnings with --strict), 2 = bad argument.
#
# What it checks:
#   1. layout: protocol-first (arp/go, arp/rust) or language-first (go/arp), names, duplicates
#   2. per protocol: code, README, spec reference (RFC/3GPP/IEEE), test command, REAL tests
#      (placeholder tests that only call Skip do not count), fuzz targets, README role of
#      each implementation when a protocol exists in several languages
#   3. repo: README, LICENSE, .gitignore, CI, stray files in the root
#   4. hygiene: tracked target/build/node_modules, large files, binaries, keys/secrets
#   5. manifests: go.mod / Cargo.toml / CMakeLists.txt, paths leaving the repo,
#      missing workspace members, add_subdirectory without CMakeLists.txt
#   6. (optional) leftovers of an old module name / path
#
# Progress table (per protocol and language), computed from the code:
#   code lines   lines of code without blank lines and comment-only lines
#   tests        real test functions (Go: Test*, Rust: #[test], C++: TEST()/add_test, Python: test_*)
#   fuzz         real fuzz targets (Go: Fuzz*, cargo-fuzz targets, proptest!, LLVMFuzzerTestOneInput)
#   to do         TO DO / FIX ME / to do!() / unimplemented!() / "not implemented" in the sources
#   ph           placeholder tests (a test body that only skips, or TestPlaceholder)
#   status       planned      fewer than --min-lines lines of code (a skeleton)
#                in progress  enough code, but no real tests or TODOs are left
#                implemented  enough code, real tests, no TODOs

set -uo pipefail

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

STRICT=0
MARKDOWN=0
MIN_LINES=40
USE_COLOR=1
[[ -t 1 ]] || USE_COLOR=0
[[ -n "${NO_COLOR:-}" ]] && USE_COLOR=0
EXTRA_IGNORE=""
STALE=()
ROOT_ARG="."

while (($#)); do
  case "$1" in
    --strict) STRICT=1 ;;
    --no-color) USE_COLOR=0 ;;
    --markdown) MARKDOWN=1 ;;
    --ignore)
      [[ $# -ge 2 ]] || { echo "--ignore needs an argument" >&2; exit 2; }
      EXTRA_IGNORE="$2"; shift ;;
    --stale)
      [[ $# -ge 2 ]] || { echo "--stale needs an argument" >&2; exit 2; }
      STALE+=("$2"); shift ;;
    --min-lines)
      [[ $# -ge 2 && "$2" =~ ^[0-9]+$ ]] || { echo "--min-lines needs a number" >&2; exit 2; }
      MIN_LINES="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "Unknown option: $1 (use --help)" >&2; exit 2 ;;
    *) ROOT_ARG="$1" ;;
  esac
  shift
done

RESOLVED=$(cd "$ROOT_ARG" 2>/dev/null && pwd -P) || { echo "No such directory: $ROOT_ARG" >&2; exit 2; }
ROOT="$RESOLVED"

# In markdown mode every normal message is discarded; only the table goes to fd 3.
if ((MARKDOWN)); then
  USE_COLOR=0
  exec 3>&1 1>/dev/null
fi

if ((USE_COLOR)); then
  C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'; C_BLU=$'\033[36m'
  C_BLD=$'\033[1m'; C_RST=$'\033[0m'
else
  C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''; C_RST=''
fi

N_OK=0; N_WARN=0; N_FAIL=0; N_INFO=0
ok()   { N_OK=$((N_OK + 1));     printf '  %s[ OK ]%s %s\n' "$C_GRN" "$C_RST" "$*"; }
warn() { N_WARN=$((N_WARN + 1)); printf '  %s[WARN]%s %s\n' "$C_YEL" "$C_RST" "$*"; }
fail() { N_FAIL=$((N_FAIL + 1)); printf '  %s[FAIL]%s %s\n' "$C_RED" "$C_RST" "$*"; }
info() { N_INFO=$((N_INFO + 1)); printf '  %s[info]%s %s\n' "$C_BLU" "$C_RST" "$*"; }
detail() { printf '%s\n' "$*" | fold -s -w 100 | sed 's/^/           /'; }
section() { printf '\n%s== %s ==%s\n' "$C_BLD" "$*" "$C_RST"; }

join_items() { # max items...
  local max="$1"; shift
  local n=$# out="" i=0 x
  for x in "$@"; do
    ((i >= max)) && break
    out+="${out:+, }$x"
    i=$((i + 1))
  done
  ((n > max)) && out+=", ... (+$((n - max)) more)"
  printf '%s' "$out"
}

# check_list LEVEL OKMSG BADMSG TOTAL ITEMS...
check_list() {
  local level="$1" okmsg="$2" badmsg="$3" total="$4"
  shift 4
  if (($# == 0)); then ok "$okmsg"; return; fi
  "$level" "$# / $total: $badmsg"
  detail "$(join_items 25 "$@")"
}

TMPD=$(mktemp -d) || exit 2
trap 'rm -rf "$TMPD"' EXIT
ALL="$TMPD/all.txt"
TRACKED="$TMPD/tracked.txt"

# --------------------------------------------------------------------------
# File list: tracked + not ignored (git), or find (no git)
# --------------------------------------------------------------------------
IS_GIT=0
if command -v git >/dev/null 2>&1 && git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  IS_GIT=1
fi

if ((IS_GIT)); then
  git -C "$ROOT" ls-files -z --cached --others --exclude-standard | tr '\0' '\n' | sort -u > "$TMPD/raw.txt"
  git -C "$ROOT" ls-files -z | tr '\0' '\n' | sort -u > "$TRACKED"
else
  (cd "$ROOT" && find . \( -name .git -o -name target -o -name node_modules -o -name build -o -name __pycache__ \) -prune -o -type f -print \
    | sed 's#^\./##' | sort -u) > "$TMPD/raw.txt"
  : > "$TRACKED"
fi
while IFS= read -r f; do
  [[ -n "$f" && -f "$ROOT/$f" ]] && printf '%s\n' "$f"
done < "$TMPD/raw.txt" > "$ALL"

if [[ ! -s "$ALL" ]]; then
  echo "No files in $ROOT - wrong directory?" >&2
  exit 2
fi

# --------------------------------------------------------------------------
# Helpers
# --------------------------------------------------------------------------
is_in() { local needle="$1"; shift; local x; for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done; return 1; }
files_in() { awk -v p="$1/" 'index($0, p) == 1' "$ALL"; }
dirs_of() { awk '{ n = split($0, a, "/"); if (n == 1) print "."; else { s = a[1]; for (i = 2; i < n; i++) s = s "/" a[i]; print s } }' | sort -u; }
file_has_i() { grep -qEi -- "$2" "$ROOT/$1" 2>/dev/null; }
find_readme() {
  awk -v p="$1/" 'index($0, p) == 1 { r = substr($0, length(p) + 1); if (r ~ /^[Rr][Ee][Aa][Dd][Mm][Ee]([.](md|rst|txt|adoc))?$/) { print $0; exit } }' "$ALL"
}
nearest_manifest() { # dir manifest -> prints the directory that holds the manifest
  local d="$1" m="$2"
  [[ "$d" == "." ]] && d=""
  while :; do
    if [[ -f "$ROOT/${d:+$d/}$m" ]]; then printf '%s' "${d:-.}"; return 0; fi
    [[ -z "$d" ]] && return 1
    if [[ "$d" == */* ]]; then d="${d%/*}"; else d=""; fi
  done
}
resolve_path() { # path -> normalized path
  if command -v realpath >/dev/null 2>&1; then realpath -m "$1" 2>/dev/null || printf '%s' "$1"
  else printf '%s' "$1"; fi
}
inside_root() { [[ "$1" == "$ROOT" || "$1" == "$ROOT"/* ]]; }

detect_langs() { # $1 = file list (newline separated)
  local fl="$1" out=""
  grep -qE '\.go$' <<<"$fl" && out+="go "
  grep -qE '(\.rs$|(^|/)Cargo\.toml$)' <<<"$fl" && out+="rust "
  if grep -qE '\.(cc|cpp|cxx|hpp|hh|hxx)$' <<<"$fl"; then out+="cpp "
  elif grep -qE '\.(c|h)$' <<<"$fl"; then out+="c "
  fi
  grep -qE '\.py$' <<<"$fl" && out+="python "
  printf '%s' "${out% }"
}

# --- code statistics -------------------------------------------------------
abs_files() { awk -v r="$ROOT" 'NF { print r "/" $0 }'; }

code_lines() { # lang file... -> lines of code (no blank lines, no comment-only lines)
  local lang="$1"; shift
  (($#)) || { echo 0; return; }
  awk -v lang="$lang" '
    FNR == 1 { inblk = 0 }
    inblk { if ($0 ~ /\*\//) inblk = 0; next }
    /^[[:space:]]*$/ { next }
    lang == "python" && /^[[:space:]]*#/ { next }
    lang != "python" && /^[[:space:]]*\/\// { next }
    lang != "python" && /^[[:space:]]*\/\*/ { if ($0 !~ /\*\//) inblk = 1; next }
    { n++ }
    END { print n + 0 }' "$@"
}

todo_count() { # file... -> number of lines with TO DO-like markers
  (($#)) || { echo 0; return; }
  cat "$@" 2>/dev/null | grep -ciE '(^|[^A-Za-z])(TODO|FIXME)([^A-Za-z]|$)|todo!\(|unimplemented!\(|not implemented|placeholder' || true
}

go_test_stats() { # _test.go files... -> "tests fuzz placeholders"
  (($#)) || { echo "0 0 0"; return; }
  awk '
    function finish() {
      if (isph) ph++
      else if (kind == "Fuzz") fz++
      else ts++
      infn = 0
    }
    FNR == 1 { if (infn) { isph = 0; finish() } }
    {
      if (!infn && $0 ~ /^func (Test|Fuzz)[A-Za-z0-9_]*\(/) {
        kind = (substr($0, 6, 4) == "Fuzz") ? "Fuzz" : "Test"
        nm = $0; sub(/^func /, "", nm); sub(/\(.*/, "", nm)
        infn = 1; blines = 0; hasskip = 0
        if ($0 ~ /\.Skip(f|Now)?\(/) hasskip = 1
        if ($0 ~ /\{/ && $0 ~ /\}[[:space:]]*$/) { isph = (nm == "TestPlaceholder" || hasskip); finish() }
        next
      }
      if (infn) {
        blines++
        if ($0 ~ /\.Skip(f|Now)?\(/) hasskip = 1
        if ($0 ~ /^\}/) { isph = (nm == "TestPlaceholder" || (hasskip && blines <= 8)); finish() }
      }
    }
    END { if (infn) { isph = 0; finish() } printf "%d %d %d\n", ts + 0, fz + 0, ph + 0 }' "$@"
}

# lang_stats LANG FILELIST -> sets S_CODE S_TESTS S_FUZZ S_PH S_TODO
lang_stats() {
  local lang="$1" fl="$2" src="" tst="" allsrc=""
  local -a _s=() _t=()
  S_CODE=0; S_TESTS=0; S_FUZZ=0; S_PH=0; S_TODO=0
  case "$lang" in
    go)
      src=$(grep -E '\.go$' <<<"$fl" | grep -vE '_test\.go$|(^|/)(testdata|vendor)/')
      tst=$(grep -E '_test\.go$' <<<"$fl" | grep -vE '(^|/)(testdata|vendor)/')
      ;;
    rust)
      allsrc=$(grep -E '\.rs$' <<<"$fl" | grep -vE '(^|/)(target|testdata)/')
      src=$(grep -vE '(^|/)(tests|benches|examples|fuzz)/' <<<"$allsrc")
      tst="$allsrc"
      ;;
    cpp|c)
      allsrc=$(grep -E '\.(cc|cpp|cxx|hpp|hh|hxx|c|h)$' <<<"$fl" | grep -vE '(^|/)(testdata|third_party|build)/')
      tst=$(grep -E '(^|/)(tests?|unittests?)/|_tests?\.(cc|cpp|cxx|c)$|(^|/)test_[^/]+\.(cc|cpp|cxx|c)$' <<<"$allsrc")
      if [[ -n "$tst" ]]; then src=$(grep -vxF -f <(printf '%s\n' "$tst") <<<"$allsrc"); else src="$allsrc"; fi
      ;;
    python)
      allsrc=$(grep -E '\.py$' <<<"$fl" | grep -vE '(^|/)(testdata|venv|\.venv)/')
      tst=$(grep -E '(^|/)tests?/|(^|/)test_[^/]+\.py$|_test\.py$' <<<"$allsrc")
      if [[ -n "$tst" ]]; then src=$(grep -vxF -f <(printf '%s\n' "$tst") <<<"$allsrc"); else src="$allsrc"; fi
      ;;
    *) return 0 ;;
  esac
  readarray -t _s < <(abs_files <<<"$src")
  readarray -t _t < <(abs_files <<<"$tst")

  S_CODE=$(code_lines "$lang" ${_s[@]+"${_s[@]}"})
  S_TODO=$(todo_count ${_s[@]+"${_s[@]}"})

  case "$lang" in
    go)
      read -r S_TESTS S_FUZZ S_PH < <(go_test_stats ${_t[@]+"${_t[@]}"})
      ;;
    rust)
      if ((${#_t[@]})); then
        S_TESTS=$(cat "${_t[@]}" | grep -cE '^[[:space:]]*#\[(tokio::|async_std::)?test[](]' || true)
        S_FUZZ=$(cat "${_t[@]}" | grep -cE 'proptest!|quickcheck!' || true)
      fi
      S_FUZZ=$((S_FUZZ + $(grep -cE '(^|/)fuzz/fuzz_targets/[^/]+\.rs$' <<<"$fl" || true)))
      ;;
    cpp|c)
      local -a _cm=()
      readarray -t _cm < <(grep -E '(^|/)CMakeLists\.txt$|\.cmake$' <<<"$fl" | abs_files)
      if ((${#_t[@]})); then
        S_TESTS=$(cat "${_t[@]}" | grep -cE '^[[:space:]]*(TEST|TEST_F|TEST_P|TYPED_TEST|TEST_CASE|SCENARIO|BOOST_AUTO_TEST_CASE)[[:space:]]*\(' || true)
      fi
      if ((${#_cm[@]})); then
        S_TESTS=$((S_TESTS + $(cat "${_cm[@]}" | grep -ciE '^[[:space:]]*add_test[[:space:]]*\(' || true)))
      fi
      if ((${#_s[@]} + ${#_t[@]})); then
        S_FUZZ=$(cat ${_s[@]+"${_s[@]}"} ${_t[@]+"${_t[@]}"} | grep -c 'LLVMFuzzerTestOneInput' || true)
      fi
      ;;
    python)
      if ((${#_t[@]})); then
        S_TESTS=$(cat "${_t[@]}" | grep -cE '^[[:space:]]*(async )?def test_' || true)
      fi
      ;;
  esac
}

status_of() { # code tests to do -> planned | in progress | implemented
  if (($1 < MIN_LINES)); then echo "planned"
  elif (($2 == 0 || $3 > 0)); then echo "in progress"
  else echo "implemented"; fi
}

# --------------------------------------------------------------------------
printf '%sRepo:%s %s  (%s files, %s)\n' "$C_BLD" "$C_RST" "$ROOT" "$(wc -l < "$ALL" | tr -d ' ')" \
  "$( ((IS_GIT)) && echo 'git: counting tracked and non-ignored files' || echo 'not a git repo: using find' )"

# --------------------------------------------------------------------------
# 1. Layout detection
# --------------------------------------------------------------------------
section "1. Directory layout"

IGNORE_DEFAULT="docs doc testdata test tests internal build target node_modules scripts tools vendor cmd examples third_party common shared lib pkg bin assets data tmp"
read -r -a IGNORE_LIST <<<"$IGNORE_DEFAULT"
if [[ -n "$EXTRA_IGNORE" ]]; then
  IFS=',' read -r -a _extra <<<"$EXTRA_IGNORE"
  IGNORE_LIST+=("${_extra[@]}")
fi
LANG_DIRS=(go rust cpp c python java)

PROTO_DIRS=()
LANGFIRST=0
PROTOFIRST=0
while IFS= read -r d; do
  [[ -z "$d" || "$d" == .* ]] && continue
  is_in "$d" "${IGNORE_LIST[@]}" && continue
  if is_in "$d" "${LANG_DIRS[@]}"; then
    LANGFIRST=1
    while IFS= read -r s; do
      [[ -z "$s" ]] && continue
      is_in "$s" "${IGNORE_LIST[@]}" && continue
      PROTO_DIRS+=("$d/$s")
    done < <(awk -F/ -v d="$d" '$1 == d && NF > 2 { print $2 }' "$ALL" | sort -u)
  else
    PROTOFIRST=1
    PROTO_DIRS+=("$d")
  fi
done < <(awk -F/ 'NF > 1 { print $1 }' "$ALL" | sort -u)

TOTAL=${#PROTO_DIRS[@]}
if ((LANGFIRST && PROTOFIRST)); then
  warn "mixed layout: language directories (go/, rust/, cpp/ ...) next to protocol directories"
  detail "Pick one layout. For a protocol repo, protocol-first is recommended: arp/go, arp/rust, bgp/cpp ..."
elif ((LANGFIRST)); then
  info "language-first layout (go/<protocol>, rust/<protocol> ...). Fine when every protocol has a single implementation."
else
  ok "protocol-first layout (<protocol>/<language>)"
fi
info "found $TOTAL protocol/project directories"
if ((TOTAL == 0)); then
  warn "no protocol directories found (check --ignore or the repo path)"
fi

# naming
BAD_NAME=(); LANG_SUFFIX=(); declare -A SEEN=()
for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
  b="${p##*/}"
  [[ "$b" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || BAD_NAME+=("$p")
  key="$b"
  if [[ "$b" =~ -(go|golang|rust|rs|cpp|cxx|c|py|python)$ ]]; then
    LANG_SUFFIX+=("$p")
    key="${b%-*}"
  fi
  [[ "$p" == */* ]] && continue # language-first: the same name in several languages is normal
  SEEN["$key"]+="$p "
done
DUPES=()
for k in "${!SEEN[@]}"; do
  read -r -a _arr <<<"${SEEN[$k]}"
  ((${#_arr[@]} >= 2)) && DUPES+=("${_arr[*]}")
done
check_list warn "directory names are kebab-case (lowercase, digits, dashes)" \
  "names not in kebab-case (use lowercase and dashes, e.g. reliable-udp)" "$TOTAL" ${BAD_NAME[@]+"${BAD_NAME[@]}"}
check_list warn "no language suffixes in names" \
  "names with a language suffix (e.g. dns-rust): use dns/rust instead of a separate directory" "$TOTAL" ${LANG_SUFFIX[@]+"${LANG_SUFFIX[@]}"}
check_list warn "no duplicated protocols" \
  "the same protocol in several directories: merge into <protocol>/<language>" "$TOTAL" ${DUPES[@]+"${DUPES[@]}"}

# --------------------------------------------------------------------------
# 2. Per protocol
# --------------------------------------------------------------------------
section "2. Protocols: code, README, tests, fuzzing"

NO_SRC=(); NO_README=(); SHORT_README=(); NO_SPEC=(); NO_RUNHINT=(); NO_TESTS=(); PH_LEFT=()
MULTI_NOPRIM=(); MIXED_FLAT=(); NO_FUZZ=()
PR_NAME=(); PR_LANG=(); PR_STATUS=(); PR_CODE=(); PR_TESTS=(); PR_FUZZ=(); PR_TODO=(); PR_PH=(); PR_README=(); PR_SPEC=()
N_IMPL=0; N_PROG=0; N_PLAN=0

push_row() { # name lang status code tests fuzz to do ph readme spec
  PR_NAME+=("$1"); PR_LANG+=("$2"); PR_STATUS+=("$3"); PR_CODE+=("$4"); PR_TESTS+=("$5")
  PR_FUZZ+=("$6"); PR_TODO+=("$7"); PR_PH+=("$8"); PR_README+=("$9"); PR_SPEC+=("${10}")
}

for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
  fl=$(files_in "$p")
  langs=$(detect_langs "$fl")
  if [[ -z "$langs" ]]; then
    NO_SRC+=("$p")
    push_row "$p" "-" "empty" 0 0 0 0 0 "-" "-"
    continue
  fi

  r_status="no"; s_status="-"
  readme=$(find_readme "$p")
  if [[ -z "$readme" ]]; then
    NO_README+=("$p")
  else
    r_status="ok"
    lines=$(wc -l < "$ROOT/$readme" | tr -d ' ')
    if ((lines < 15)); then SHORT_README+=("$p ($lines lines)"); r_status="short"; fi
    if file_has_i "$readme" 'RFC ?[0-9]+|3GPP|TS ?[0-9]{2}\.[0-9]{3}|IEEE|ITU|ISO/IEC|X\.[0-9]{3}|BCP ?[0-9]+|draft-|OASIS|specification'; then
      s_status="yes"
    else
      s_status="no"; NO_SPEC+=("$p")
    fi
    file_has_i "$readme" 'go test|cargo test|ctest|cmake|make test|make check|pytest|go run|cargo run' || NO_RUNHINT+=("$p")
    nl=$(wc -w <<<"$langs" | tr -d ' ')
    if ((nl >= 2)); then
      file_has_i "$readme" 'primary|reference impl|main impl|interop|peer|cross-test|each other' || MULTI_NOPRIM+=("$p ($langs)")
    fi
  fi

  # several languages mixed in one flat directory
  flat=$(awk -v p="$p/" 'index($0, p) == 1 { r = substr($0, length(p) + 1); if (r !~ /\//) print r }' <<<"$fl")
  nflat=0
  grep -qE '\.go$' <<<"$flat" && nflat=$((nflat + 1))
  grep -qE '\.rs$' <<<"$flat" && nflat=$((nflat + 1))
  grep -qE '\.(cc|cpp|cxx|hpp)$' <<<"$flat" && nflat=$((nflat + 1))
  ((nflat >= 2)) && MIXED_FLAT+=("$p")

  # statistics per language
  proto_fuzz=0
  for l in $langs; do
    lang_stats "$l" "$fl"
    st=$(status_of "$S_CODE" "$S_TESTS" "$S_TODO")
    case "$st" in
      implemented) N_IMPL=$((N_IMPL + 1)) ;;
      "in progress") N_PROG=$((N_PROG + 1)) ;;
      *) N_PLAN=$((N_PLAN + 1)) ;;
    esac
    if ((S_TESTS == 0)); then
      if ((S_PH > 0)); then NO_TESTS+=("$p ($l: $S_PH placeholder tests only)"); else NO_TESTS+=("$p ($l)"); fi
    elif ((S_PH > 0)); then
      PH_LEFT+=("$p ($l: $S_PH)")
    fi
    proto_fuzz=$((proto_fuzz + S_FUZZ))
    push_row "$p" "$l" "$st" "$S_CODE" "$S_TESTS" "$S_FUZZ" "$S_TODO" "$S_PH" "$r_status" "$s_status"
  done
  ((proto_fuzz == 0)) && NO_FUZZ+=("$p")
done

check_list warn "every directory contains source code" \
  "directories without source code (move to docs/ or delete)" "$TOTAL" ${NO_SRC[@]+"${NO_SRC[@]}"}
check_list warn "every protocol has a README" \
  "protocols without README (at least: which RFC, what works, what is missing, how to run the tests)" "$TOTAL" ${NO_README[@]+"${NO_README[@]}"}
check_list warn "protocol READMEs have a sensible length" \
  "READMEs shorter than 15 lines" "$TOTAL" ${SHORT_README[@]+"${SHORT_README[@]}"}
check_list warn "READMEs cite the RFC / specification" \
  "READMEs without a reference to an RFC/3GPP/IEEE document (a reader wants to see what you implement against)" "$TOTAL" ${NO_SPEC[@]+"${NO_SPEC[@]}"}
check_list warn "READMEs say how to run the tests/build" \
  "READMEs without a run command (go test / cargo test / ctest ...)" "$TOTAL" ${NO_RUNHINT[@]+"${NO_RUNHINT[@]}"}
check_list warn "multi-language protocols describe the role of each implementation" \
  "several languages, but the README does not say which one is primary or how they are cross-tested (looking for: primary/reference/interop)" "$TOTAL" ${MULTI_NOPRIM[@]+"${MULTI_NOPRIM[@]}"}
check_list warn "languages are separated inside protocol directories" \
  "files of different languages in one flat directory: split into <protocol>/go, <protocol>/rust, <protocol>/cpp" "$TOTAL" ${MIXED_FLAT[@]+"${MIXED_FLAT[@]}"}
check_list warn "every (protocol, language) has real tests" \
  "(protocol, language) entries without real tests (placeholders that only call Skip do not count)" "${#PR_NAME[@]}" ${NO_TESTS[@]+"${NO_TESTS[@]}"}
check_list warn "no leftover placeholder tests" \
  "entries still containing placeholder tests (remove them or write the real test)" "${#PR_NAME[@]}" ${PH_LEFT[@]+"${PH_LEFT[@]}"}
check_list info "every protocol has real fuzz targets" \
  "protocols without real fuzz targets (go test -fuzz, cargo fuzz, libFuzzer): packet parsers are an ideal target" "$TOTAL" ${NO_FUZZ[@]+"${NO_FUZZ[@]}"}

if ((${#PR_NAME[@]} > 0)); then
  printf '\n  %-20s %-6s %-12s %5s %5s %4s %4s %3s %-6s %-4s\n' "project" "lang" "status" "code" "tests" "fuzz" "todo" "ph" "readme" "spec"
  printf '  %-20s %-6s %-12s %5s %5s %4s %4s %3s %-6s %-4s\n' "--------------------" "------" "------------" "-----" "-----" "----" "----" "---" "------" "----"
  for i in "${!PR_NAME[@]}"; do
    printf '  %-20s %-6s %-12s %5s %5s %4s %4s %3s %-6s %-4s\n' "${PR_NAME[$i]}" "${PR_LANG[$i]}" "${PR_STATUS[$i]}" "${PR_CODE[$i]}" \
      "${PR_TESTS[$i]}" "${PR_FUZZ[$i]}" "${PR_TODO[$i]}" "${PR_PH[$i]}" "${PR_README[$i]}" "${PR_SPEC[$i]}"
  done
  printf '\n  code = lines of code without blanks/comments, ph = placeholder tests (skip only), planned = under %s lines of code\n' "$MIN_LINES"
  printf '  %sProgress:%s %d implemented, %d in progress, %d planned (of %d entries)\n' "$C_BLD" "$C_RST" "$N_IMPL" "$N_PROG" "$N_PLAN" "${#PR_NAME[@]}"
  if ((N_PLAN > 0)); then
    info "$N_PLAN / ${#PR_NAME[@]} entries are skeletons (fewer than $MIN_LINES lines of code): mark them 'planned' in the README until they have real code"
  fi
fi

print_markdown() {
  printf '### Protocol progress\n\n'
  printf '| Protocol | Lang | Status | Code lines | Tests | Fuzz | TODO | Placeholders |\n'
  printf '|---|---|---|--:|--:|--:|--:|--:|\n'
  local i
  for i in "${!PR_NAME[@]}"; do
    printf '| %s | %s | %s | %s | %s | %s | %s | %s |\n' "${PR_NAME[$i]}" "${PR_LANG[$i]}" "${PR_STATUS[$i]}" "${PR_CODE[$i]}" \
      "${PR_TESTS[$i]}" "${PR_FUZZ[$i]}" "${PR_TODO[$i]}" "${PR_PH[$i]}"
  done
  printf '\n**%d implemented, %d in progress, %d planned** (planned = fewer than %s lines of code; placeholders are skip-only tests and do not count as tests).\n' \
    "$N_IMPL" "$N_PROG" "$N_PLAN" "$MIN_LINES"
}

if ((MARKDOWN)); then
  print_markdown >&3
  exit 0
fi

# --------------------------------------------------------------------------
# 3. Repository top level
# --------------------------------------------------------------------------
section "3. Repository: top-level files"

if [[ -f "$ROOT/README.md" ]]; then
  ok "README.md exists"
  NOT_IN_README=()
  for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
    grep -qiF -- "${p##*/}" "$ROOT/README.md" || NOT_IN_README+=("$p")
  done
  check_list warn "every protocol is listed in the main README" \
    "protocols missing from the main README (the README should be an index with links)" "$TOTAL" ${NOT_IN_README[@]+"${NOT_IN_README[@]}"}
else
  fail "no README.md in the repo root"
fi

if grep -qiE '^(LICENSE|LICENCE|COPYING)(\.(md|txt))?$' "$ALL"; then ok "LICENSE file exists"; else warn "no LICENSE file (without a license nobody can legally use the code)"; fi

if [[ -f "$ROOT/.gitignore" ]]; then
  ok ".gitignore exists"
  if grep -qE '(^|/)Cargo\.toml$' "$ALL" && ! grep -qE '^/?(\*\*/)?target/?\*?$' "$ROOT/.gitignore"; then
    warn ".gitignore does not ignore target/ (the repo contains Rust)"
  fi
  if grep -qE '(^|/)CMakeLists\.txt$' "$ALL" && ! grep -qE '^/?(\*\*/)?(build|cmake-build-\*|out)[*/]*$' "$ROOT/.gitignore"; then
    warn ".gitignore does not ignore build/ (the repo contains CMake)"
  fi
else
  warn "no .gitignore"
fi

if grep -qE '^\.github/workflows/[^/]+\.ya?ml$|^\.gitlab-ci\.yml$' "$ALL"; then
  ok "CI configuration exists"
else
  warn "no CI (.github/workflows/*.yml): a green badge with passing tests is the cheapest quality signal"
fi

STRAY=$(grep -E '^[^/]+\.(pdf|zip|tar|tgz|gz|7z|rar|docx?|pptx?|xlsx?|log|exe|dmg|iso)$' "$ALL")
if [[ -n "$STRAY" ]]; then
  readarray -t _stray <<<"$STRAY"
  warn "${#_stray[@]} stray files in the root (move to docs/ or delete)"
  detail "$(join_items 25 "${_stray[@]}")"
else
  ok "no stray files (pdf/zip/log...) in the root"
fi

# --------------------------------------------------------------------------
# 4. Hygiene
# --------------------------------------------------------------------------
section "4. Hygiene: junk, large files, binaries, secrets"

if ((IS_GIT)); then
  readarray -t HEAVY < <(grep -E '(^|/)(target|build|cmake-build-[^/]+|node_modules|__pycache__)/|(^|/)\.DS_Store$|\.(o|a|so|dylib|obj|exe|class|pyc)$' "$TRACKED")
  if ((${#HEAVY[@]})); then
    fail "${#HEAVY[@]} tracked build/binary files (git rm -r --cached <path> and add it to .gitignore)"
    detail "$(join_items 15 "${HEAVY[@]}")"
  else
    ok "git tracks no target/ build/ node_modules/ or object files"
  fi
  readarray -t EDITOR < <(grep -E '(^|/)(\.idea|\.vscode)/' "$TRACKED")
  if ((${#EDITOR[@]})); then
    warn "${#EDITOR[@]} tracked editor files (.idea/.vscode): add them to .gitignore unless that is deliberate"
  fi
else
  info "not a git repo: skipping the tracked-files checks"
  for d in target build node_modules; do
    [[ -d "$ROOT/$d" ]] && warn "directory $d/ exists on disk: make sure it is in .gitignore"
  done
fi

# large files
BIG_WARN=(); BIG_FAIL=()
if stat -c '%s' /dev/null >/dev/null 2>&1; then
  while read -r size name; do
    mb=$(awk -v s="$size" 'BEGIN { printf "%.1f", s / 1048576 }')
    if ((size > 52428800)); then BIG_FAIL+=("$name (${mb} MB)")
    elif ((size > 1048576)); then BIG_WARN+=("$name (${mb} MB)"); fi
  done < <(cd "$ROOT" && tr '\n' '\0' < "$ALL" | xargs -0 stat -c '%s %n' 2>/dev/null)
else
  while IFS= read -r f; do
    size=$(wc -c < "$ROOT/$f" | tr -d ' ')
    mb=$(awk -v s="$size" 'BEGIN { printf "%.1f", s / 1048576 }')
    if ((size > 52428800)); then BIG_FAIL+=("$f (${mb} MB)")
    elif ((size > 1048576)); then BIG_WARN+=("$f (${mb} MB)"); fi
  done < "$ALL"
fi
if ((${#BIG_FAIL[@]})); then
  fail "${#BIG_FAIL[@]} files > 50 MB (GitHub rejects > 100 MB)"; detail "$(join_items 10 "${BIG_FAIL[@]}")"
fi
if ((${#BIG_WARN[@]})); then
  warn "${#BIG_WARN[@]} files > 1 MB (pcaps, binaries, data?)"; detail "$(join_items 10 "${BIG_WARN[@]}")"
fi
((${#BIG_FAIL[@]} == 0 && ${#BIG_WARN[@]} == 0)) && ok "no files larger than 1 MB"

# executables
is_native_binary() {
  local magic
  magic=$(head -c4 "$ROOT/$1" 2>/dev/null | od -An -tx1 | tr -d ' \n')
  case "$magic" in
    7f454c46|cffaedfe|cefaedfe|feedface|feedfacf|4d5a*) return 0 ;;
  esac
  return 1
}
BINS=()
while IFS= read -r f; do
  base="${f##*/}"
  if [[ -x "$ROOT/$f" || "$base" != *.* || "$base" =~ \.(exe|dll|o|a|so|dylib|out|bin|test)$ ]]; then
    is_native_binary "$f" && BINS+=("$f")
  fi
done < "$ALL"
check_list fail "no compiled binaries in the tree" \
  "compiled binaries (ELF/Mach-O/PE): delete them and add them to .gitignore" "$(wc -l < "$ALL" | tr -d ' ')" ${BINS[@]+"${BINS[@]}"}

# secrets (never prints values, only file:line)
SECRET_HITS=$(cd "$ROOT" && tr '\n' '\0' < "$ALL" | xargs -0 grep -InE \
  -e '-----BEGIN ((RSA|EC|DSA|OPENSSH|PGP|ENCRYPTED) )?PRIVATE KEY' \
  -e 'AKIA[0-9A-Z]{16}' -e 'gh[pousr]_[A-Za-z0-9]{36,}' -e 'xox[baprs]-[0-9A-Za-z-]{10,}' -e 'sk-[A-Za-z0-9]{32,}' \
  2>/dev/null | cut -d: -f1,2 | grep -vE 'check-(layout|build)\.sh' || true)
if [[ -n "$SECRET_HITS" ]]; then
  readarray -t _hits <<<"$SECRET_HITS"
  S_TEST=(); S_REAL=()
  for h in "${_hits[@]}"; do
    if [[ "$h" =~ (^|/)(testdata|test|tests|fixtures|examples)/ ]]; then S_TEST+=("$h"); else S_REAL+=("$h"); fi
  done
  if ((${#S_REAL[@]})); then
    fail "${#S_REAL[@]} suspected keys/tokens outside test directories (delete, revoke the key, clean the git history)"
    detail "$(join_items 10 "${S_REAL[@]}")"
  fi
  if ((${#S_TEST[@]})); then
    warn "${#S_TEST[@]} keys in test directories: make sure they are throwaway keys generated only for tests"
    detail "$(join_items 10 "${S_TEST[@]}")"
  fi
else
  ok "no private keys or tokens found (AWS/GitHub/Slack)"
fi
info "passwords in protocol code (e.g. a RADIUS shared secret) are ignored here: only well-known token patterns are checked"

# --------------------------------------------------------------------------
# 5. Manifests and dependencies (static)
# --------------------------------------------------------------------------
section "5. Manifests and dependencies (no toolchain needed)"

# --- Go
GO_FILES=$(grep -E '\.go$' "$ALL" | grep -vE '(^|/)(testdata|vendor)/' || true)
if [[ -n "$GO_FILES" ]]; then
  ORPHAN_GO=()
  while IFS= read -r d; do
    nearest_manifest "$d" go.mod >/dev/null || ORPHAN_GO+=("$d")
  done < <(dirs_of <<<"$GO_FILES")
  check_list fail "every directory with .go files belongs to a module (go.mod)" \
    "directories with .go files and no go.mod above them (after splitting a repo go.mod is often missing: go mod init ...)" \
    "$(dirs_of <<<"$GO_FILES" | wc -l | tr -d ' ')" ${ORPHAN_GO[@]+"${ORPHAN_GO[@]}"}

  readarray -t GOMODS < <(grep -E '(^|/)go\.mod$' "$ALL" | grep -vE '(^|/)(testdata|vendor)/')
  declare -A MODSEEN=()
  BAD_MODPATH=(); NO_GODIR=(); BAD_REPLACE=(); NO_GOSUM=()
  for gm in ${GOMODS[@]+"${GOMODS[@]}"}; do
    mdir=$(dirname "$gm")
    mpath=$(awk '/^module[ \t]/ { gsub(/"/, "", $2); print $2; exit }' "$ROOT/$gm")
    info "Go module: ${mpath:-<none>} (${mdir})"
    [[ -z "$mpath" ]] && { BAD_MODPATH+=("$gm (no module line)"); continue; }
    [[ "${mpath%%/*}" == *.* ]] || BAD_MODPATH+=("$mpath")
    MODSEEN["$mpath"]+="$gm "
    grep -qE '^go [0-9]' "$ROOT/$gm" || NO_GODIR+=("$gm")
    if grep -qE '^[[:space:]]*(require|[a-zA-Z0-9._~/-]+[[:space:]]+v[0-9])' "$ROOT/$gm"; then
      [[ -f "$ROOT/$mdir/go.sum" ]] || NO_GOSUM+=("$gm")
    fi
    while IFS= read -r line; do
      tgt=$(sed -E 's/.*=>[[:space:]]*([^[:space:]]+).*/\1/' <<<"$line")
      abs=$(resolve_path "$ROOT/$mdir/$tgt")
      if ! inside_root "$abs"; then BAD_REPLACE+=("$gm: => $tgt (outside the repo)")
      elif [[ ! -d "$abs" ]]; then BAD_REPLACE+=("$gm: => $tgt (does not exist)"); fi
    done < <(grep -E '=>[[:space:]]*\.{1,2}/' "$ROOT/$gm" || true)
  done
  check_list warn "module paths look like host/user/repo" \
    "module paths without a domain (go get will not work; use e.g. github.com/user/repo)" "${#GOMODS[@]}" ${BAD_MODPATH[@]+"${BAD_MODPATH[@]}"}
  check_list warn "go.mod files have a go directive" "go.mod without a 'go 1.xx' directive" "${#GOMODS[@]}" ${NO_GODIR[@]+"${NO_GODIR[@]}"}
  check_list fail "go.mod files with dependencies have go.sum" "go.mod with dependencies but no go.sum (go mod tidy)" "${#GOMODS[@]}" ${NO_GOSUM[@]+"${NO_GOSUM[@]}"}
  check_list fail "no local replace directives leaving the repo" \
    "local replace in go.mod that will break once the repo is split" "${#GOMODS[@]}" ${BAD_REPLACE[@]+"${BAD_REPLACE[@]}"}
  DUP_MODS=()
  for k in "${!MODSEEN[@]}"; do
    read -r -a _m <<<"${MODSEEN[$k]}"
    ((${#_m[@]} >= 2)) && DUP_MODS+=("$k: ${_m[*]}")
  done
  check_list fail "every Go module has a unique path" "the same module path in several go.mod files" "${#GOMODS[@]}" ${DUP_MODS[@]+"${DUP_MODS[@]}"}
else
  info "no Go code"
fi

# --- Rust
readarray -t CARGOS < <(grep -E '(^|/)Cargo\.toml$' "$ALL" | grep -vE '(^|/)(testdata|target)/')
if ((${#CARGOS[@]})); then
  RS_BAD_PATH=(); RS_BAD_MEMBER=(); RS_NO_LOCK=()
  for cm in "${CARGOS[@]}"; do
    cdir=$(dirname "$cm")
    # path = "..." dependencies (not leaving the repo)
    while IFS= read -r line; do
      tgt=$(sed -E 's/.*path[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/' <<<"$line")
      abs=$(resolve_path "$ROOT/$cdir/$tgt")
      if ! inside_root "$abs"; then RS_BAD_PATH+=("$cm: path=\"$tgt\" (outside the repo)")
      elif [[ ! -e "$abs" ]]; then RS_BAD_PATH+=("$cm: path=\"$tgt\" (does not exist)"); fi
    done < <(grep -E 'path[[:space:]]*=[[:space:]]*"[^"]+"' "$ROOT/$cm" | grep -vE '^[[:space:]]*#' || true)
    # workspace members
    if grep -qE '^\[workspace\]' "$ROOT/$cm"; then
      while IFS= read -r m; do
        [[ -z "$m" ]] && continue
        matched=0
        for g in "$ROOT/$cdir"/$m; do
          if [[ -f "$g/Cargo.toml" ]]; then matched=1; fi
        done
        ((matched)) || RS_BAD_MEMBER+=("$cm: member \"$m\" (no directory with Cargo.toml)")
      done < <(awk '
        /^\[workspace\]/ { inws = 1; next }
        /^\[/ && !/^\[workspace/ { inws = 0 }
        inws && /members[[:space:]]*=/ { inm = 1 }
        inm { line = $0
              while (match(line, /"[^"]+"/)) { print substr(line, RSTART + 1, RLENGTH - 2); line = substr(line, RSTART + RLENGTH) }
              if ($0 ~ /\]/) inm = 0 }' "$ROOT/$cm")
      nearest_manifest "$cdir" Cargo.lock >/dev/null || RS_NO_LOCK+=("$cdir (workspace)")
    elif [[ -f "$ROOT/$cdir/src/main.rs" ]] || grep -qE '^\[\[bin\]\]' "$ROOT/$cm"; then
      # binary: Cargo.lock in the crate directory or in a workspace directory above it
      nearest_manifest "$cdir" Cargo.lock >/dev/null || RS_NO_LOCK+=("$cdir (binary)")
    fi
  done
  check_list fail "Cargo path dependencies point to existing directories inside the repo" \
    "path=... dependencies outside the repo or missing (they will break once the repo is split)" "${#CARGOS[@]}" ${RS_BAD_PATH[@]+"${RS_BAD_PATH[@]}"}
  check_list fail "workspace members exist" "workspace members without a directory" "${#CARGOS[@]}" ${RS_BAD_MEMBER[@]+"${RS_BAD_MEMBER[@]}"}
  check_list warn "workspaces and binaries have Cargo.lock" "missing Cargo.lock (keep it in the repo for applications/workspaces)" "${#CARGOS[@]}" ${RS_NO_LOCK[@]+"${RS_NO_LOCK[@]}"}
else
  info "no Rust code"
fi

# --- CMake
readarray -t CMAKES < <(grep -E '(^|/)CMakeLists\.txt$' "$ALL" | grep -vE '(^|/)(testdata|build)/')
if ((${#CMAKES[@]})); then
  CM_MISSING=(); CM_ORPHAN=(); CM_NOMIN=()
  declare -A SUBS=()
  for cm in "${CMAKES[@]}"; do
    cdir=$(dirname "$cm")
    stripped=$(sed 's/#.*//' "$ROOT/$cm")
    if grep -qi 'project[[:space:]]*(' <<<"$stripped" && ! grep -qi 'cmake_minimum_required' <<<"$stripped"; then
      CM_NOMIN+=("$cm")
    fi
    while IFS= read -r sub; do
      [[ -z "$sub" || "$sub" == *'$'* ]] && continue
      SUBS["$cdir"]+="$sub "
      target="$cdir/$sub"; [[ "$cdir" == "." ]] && target="$sub"
      [[ -f "$ROOT/$target/CMakeLists.txt" ]] || CM_MISSING+=("$cm: add_subdirectory($sub) without CMakeLists.txt")
    done < <(grep -oiE 'add_subdirectory[[:space:]]*\([[:space:]]*"?[^) "]+' <<<"$stripped" | sed -E 's/.*\([[:space:]]*"?//')
  done
  for cm in "${CMAKES[@]}"; do
    cdir=$(dirname "$cm")
    [[ "$cdir" == "." ]] && continue
    parent=$(dirname "$cdir")
    [[ -f "$ROOT/$parent/CMakeLists.txt" || ( "$parent" == "." && -f "$ROOT/CMakeLists.txt" ) ]] || continue
    rel="${cdir#"$parent"/}"; [[ "$parent" == "." ]] && rel="$cdir"
    case " ${SUBS[$parent]:-} " in *" $rel "*) ;; *) CM_ORPHAN+=("$cm (no add_subdirectory($rel) in $parent/CMakeLists.txt)") ;; esac
  done
  check_list fail "add_subdirectory points to existing directories" "add_subdirectory to directories without CMakeLists.txt" "${#CMAKES[@]}" ${CM_MISSING[@]+"${CM_MISSING[@]}"}
  check_list warn "every nested CMakeLists is included via add_subdirectory" "nested CMakeLists not used by the parent" "${#CMAKES[@]}" ${CM_ORPHAN[@]+"${CM_ORPHAN[@]}"}
  check_list warn "CMake projects have cmake_minimum_required" "project() without cmake_minimum_required" "${#CMAKES[@]}" ${CM_NOMIN[@]+"${CM_NOMIN[@]}"}
else
  if grep -qE '\.(cc|cpp|cxx|hpp)$' "$ALL"; then
    warn "there are C++ files but no CMakeLists.txt anywhere"
  else
    info "no C++ code"
  fi
fi

# --------------------------------------------------------------------------
# 6. Leftovers of an old name
# --------------------------------------------------------------------------
if ((${#STALE[@]})); then
  section "6. Leftovers of the old name / path"
  for re in "${STALE[@]}"; do
    HITS=$(cd "$ROOT" && tr '\n' '\0' < "$ALL" | xargs -0 grep -InE -- "$re" 2>/dev/null | grep -vE 'check-(layout|build)\.sh' || true)
    if [[ -z "$HITS" ]]; then
      ok "no occurrences of: $re"
    else
      cnt=$(wc -l <<<"$HITS" | tr -d ' ')
      warn "$cnt occurrences of: $re (after splitting a repo these are usually old imports / README links)"
      head -n 12 <<<"$HITS" | cut -c1-140 | sed 's/^/           /'
    fi
  done
fi

# --------------------------------------------------------------------------
printf '\n%sSummary:%s %s%d OK%s, %s%d warnings%s, %s%d errors%s, %d hints\n' "$C_BLD" "$C_RST" \
  "$C_GRN" "$N_OK" "$C_RST" "$C_YEL" "$N_WARN" "$C_RST" "$C_RED" "$N_FAIL" "$C_RST" "$N_INFO"
if ((N_FAIL > 0)); then exit 1; fi
if ((STRICT && N_WARN > 0)); then exit 1; fi
exit 0
