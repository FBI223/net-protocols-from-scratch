#!/usr/bin/env bash
#
# check-build.sh - sprawdza zaleznosci i budowanie projektow Go / Rust / C++ w repo.
# Wymaga zainstalowanych toolchainow (go, cargo, cmake + kompilator); brakujace pomija.
# Repo zostaje nietkniete: build C++ idzie do katalogu tymczasowego, target/ Rusta do
# ~/.cache/repo-checks/ (mozna zmienic przez CARGO_TARGET_DIR), a go.mod/go.sum sa po
# kontroli 'go mod tidy' przywracane (na Go >= 1.23 uzywane jest 'go mod tidy -diff').
#
# Uzycie:
#   ./check-build.sh [opcje] [sciezka-do-repo]     (domyslnie: biezacy katalog)
#
# Opcje:
#   --only go,rust,cpp   sprawdz tylko wybrane jezyki
#   --skip-tests         nie uruchamiaj testow (testy sieciowe moga wymagac portow/uprawnien)
#   --clippy             dodatkowo cargo clippy -D warnings
#   --vuln               dodatkowo govulncheck / cargo audit (jesli zainstalowane)
#   --write-locks        pozwol cargo zostawic nowo wygenerowane Cargo.lock w repo (domyslnie sa usuwane)
#   --timeout SEC        limit czasu pojedynczego kroku (domyslnie 600)
#   --jobs N             rownoleglosc budowania C++ (domyslnie: liczba rdzeni)
#   -v, --verbose        pokaz pelny output kazdego kroku
#   --no-color           wylacz kolory
#   -h, --help           ta pomoc
#
# Kod wyjscia: 0 = wszystko ok, 1 = sa bledy, 2 = zly argument.
#
# Co sprawdza:
#   Go    : wersja z go.mod vs lokalna, go mod tidy (czy go.mod/go.sum sa aktualne),
#           go mod verify, go build ./..., go vet ./..., gofmt, go test ./...
#   Rust  : cargo metadata (czy kazdy crate nalezy do workspace, sciezki path= w repo),
#           cargo fmt --check, cargo check --locked (aktualny Cargo.lock), cargo test
#   C++   : cmake configure + build kazdego glownego CMakeLists.txt (z -Wall -Wextra),
#           liczba ostrzezen kompilatora, ctest (i ostrzezenie, gdy testow brak)

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

need_arg() { [[ $# -ge 2 ]] || { echo "$1 wymaga argumentu" >&2; exit 2; }; }
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
    -*) echo "Nieznana opcja: $1 (uzyj --help)" >&2; exit 2 ;;
    *) ROOT_ARG="$1" ;;
  esac
  shift
done

[[ "$TIMEOUT" =~ ^[0-9]+$ ]] || { echo "--timeout wymaga liczby sekund" >&2; exit 2; }
RESOLVED=$(cd "$ROOT_ARG" 2>/dev/null && pwd -P) || { echo "Nie ma takiego katalogu: $ROOT_ARG" >&2; exit 2; }
ROOT="$RESOLVED"
ROOT_ESC=$(printf '%s' "$ROOT" | sed 's/[][\.*^$#/]/\\&/g')
IS_TTY=0; [[ -t 1 ]] && IS_TTY=1
# logi krokow sa przechwytywane, wiec kolorow narzedzi nie chcemy
export CARGO_TERM_COLOR=never NO_COLOR=1
# target/ Rusta ma trafiac poza repo (repo zostaje czyste), ale do trwalego cache, zeby kolejne uruchomienia byly szybkie
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
FAILED=()
CTX="repo"
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
# shellcheck disable=SC2329  # wywolywana przez trap
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
    detail "wyglada na problem z siecia: pierwsze budowanie pobiera zaleznosci (sprawdz polaczenie / proxy)"
  return 0
}

# run_step LABEL DIR CMD...  -> 0 gdy ok; na bledzie wypisuje koniec logu
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
  if ((rc == 124)); then fail "$label: przekroczono limit ${TIMEOUT}s"; else fail "$label (kod $rc, ${dt}s)"; fi
  FAILED+=("$CTX: $label")
  tail -n 25 "$LAST_LOG" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
  net_hint
  return "$rc"
}

find_manifests() { # nazwa -> sciezki bezwzgledne, plytsze najpierw
  find "$ROOT" \( -name .git -o -name target -o -name node_modules -o -name vendor -o -name testdata \
      -o -name build -o -name 'cmake-build-*' \) -prune -o -type f -name "$1" -print \
    | awk '{ n = gsub("/", "/"); printf "%d\t%s\n", n, $0 }' | LC_ALL=C sort -t "$(printf '\t')" -k1,1n -k2,2 | cut -f2-
}
has_bin() { # katalog crate'a/workspace zawiera binarke?
  [[ -f "$1/src/main.rs" ]] || grep -qE '^\[\[bin\]\]' "$1/Cargo.toml" 2>/dev/null \
    || find "$1" -maxdepth 3 -path '*/src/main.rs' -not -path '*/target/*' 2>/dev/null | grep -q .
}
rel_of() { local r="${1#"$ROOT"}"; r="${r#/}"; printf '%s' "${r:-.}"; }
inside_root() { [[ "$1" == "$ROOT" || "$1" == "$ROOT"/* ]]; }

printf '%sRepo:%s %s\n' "$C_BLD" "$C_RST" "$ROOT"
printf 'Narzedzia: go=%s cargo=%s cmake=%s  (jobs=%s, timeout=%ss%s)\n' \
  "$(have go && echo tak || echo nie)" "$(have cargo && echo tak || echo nie)" "$(have cmake && echo tak || echo nie)" \
  "$JOBS" "$TIMEOUT" "$( ((SKIP_TESTS)) && echo ', bez testow' || true)"

# ==========================================================================
# GO
# ==========================================================================
if wants go; then
  section "Go"
  readarray -t GOMODS < <(find_manifests go.mod)
  if ((${#GOMODS[@]} == 0)); then
    info "brak go.mod w repo"
  elif ! have go; then
    skip "go nie jest zainstalowany: pomijam ${#GOMODS[@]} modulow"
  else
    GO_LOCAL=$(go env GOVERSION 2>/dev/null); GO_LOCAL="${GO_LOCAL#go}"
    HAS_TIDY_DIFF=0
    go help mod tidy 2>&1 | grep -q -- '-diff' && HAS_TIDY_DIFF=1
    [[ "${CHECK_BUILD_FORCE_TIDY_FALLBACK:-0}" == 1 ]] && HAS_TIDY_DIFF=0 # tylko do testow skryptu
    for gm in "${GOMODS[@]}"; do
      dir=$(dirname "$gm"); rel=$(rel_of "$dir")
      mpath=$(awk '/^module[ \t]/ { gsub(/"/, "", $2); print $2; exit }' "$gm")
      CTX="go:$rel"
      subsection "modul ${mpath:-?} (${rel})"

      need=$(awk '/^go[ \t]+[0-9]/ { print $2; exit }' "$gm")
      if [[ -n "$need" && -n "$GO_LOCAL" && "$(printf '%s\n%s\n' "$need" "$GO_LOCAL" | sort -V | head -n1)" != "$need" ]]; then
        warn "go.mod wymaga go $need, lokalnie jest $GO_LOCAL (go moze pobrac nowszy toolchain albo build sie nie uda)"
      fi

      # lokalne replace wychodzace poza repo
      while IFS= read -r line; do
        tgt=$(sed -E 's/.*=>[[:space:]]*([^[:space:]]+).*/\1/' <<<"$line")
        abs=$(realpath -m "$dir/$tgt" 2>/dev/null || echo "$dir/$tgt")
        if ! inside_root "$abs"; then fail "replace => $tgt wskazuje poza repo (po rozdzieleniu repo przestanie dzialac)"; FAILED+=("$CTX: replace poza repo")
        elif [[ ! -d "$abs" ]]; then fail "replace => $tgt: katalog nie istnieje"; FAILED+=("$CTX: replace nie istnieje"); fi
      done < <(grep -E '=>[[:space:]]*\.{1,2}/' "$gm" || true)

      # go mod tidy: czy go.mod/go.sum sa aktualne
      if ((HAS_TIDY_DIFF)); then
        run_step "go mod tidy -diff (go.mod/go.sum aktualne)" "$dir" go mod tidy -diff \
          || detail "napraw: (cd $rel && go mod tidy)"
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
          fail "go mod tidy nie przeszlo (kod $trc)"; FAILED+=("$CTX: go mod tidy")
          tail -n 15 "$LAST_LOG" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
          net_hint
        elif ((changed)); then
          fail "go.mod/go.sum nie sa w stanie 'tidy' (zmiany cofniete)"; FAILED+=("$CTX: go mod tidy")
          detail "napraw: (cd $rel && go mod tidy)"
        else
          ok "go mod tidy: go.mod/go.sum aktualne"
        fi
      fi

      run_step "go mod verify" "$dir" go mod verify
      run_step "go build ./..." "$dir" go build ./... \
        || detail "typowe po rozdzieleniu repo: 'use of internal package ... not allowed' lub import ze starej sciezki modulu"
      run_step "go vet ./..." "$dir" go vet ./...

      unformatted=$(cd "$dir" && gofmt -l . 2>&1 | grep -v '^$' || true)
      if [[ -n "$unformatted" ]]; then
        readarray -t _uf <<<"$unformatted"
        warn "${#_uf[@]} plikow bez gofmt (napraw: (cd $rel && gofmt -w .))"
        detail "$(printf '%s, ' "${_uf[@]:0:15}" | sed 's/, $//')"
      else
        ok "gofmt: brak uwag"
      fi

      if ((SKIP_TESTS)); then
        skip "go test (--skip-tests)"
      else
        run_step "go test ./..." "$dir" go test ./... -count=1 -timeout 120s \
          || detail "jesli to testy sieciowe: moga wymagac wolnych portow/uprawnien (sprobuj --skip-tests)"
      fi

      if ((VULN)); then
        if have govulncheck; then run_step "govulncheck ./..." "$dir" govulncheck ./...
        else skip "govulncheck niezainstalowany (go install golang.org/x/vuln/cmd/govulncheck@latest)"; fi
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
    info "brak Cargo.toml w repo"
  elif ! have cargo; then
    skip "cargo nie jest zainstalowany: pomijam ${#CARGOS[@]} manifestow"
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
          skip "ten sam blad workspace co dla ${SEEN_ERR[$errsum]} (nie powtarzam)"
          continue
        fi
        SEEN_ERR["$errsum"]="$rel"
        fail "cargo metadata nie przeszlo"; FAILED+=("$CTX: cargo metadata")
        head -n 8 "$meta.err" | sed "s#${ROOT_ESC}/##g" | cut -c1-160 | sed 's/^/           | /'
        if grep -q "believes it's in a workspace when it's not" "$meta.err"; then
          detail "ten crate lezy pod katalogiem workspace, ale nie jest w jego 'members': dodaj go do members (lub do exclude), albo daj mu wlasne puste [workspace] w Cargo.toml"
        elif grep -q 'failed to load manifest for dependency\|failed to read .*Cargo.toml' "$meta.err"; then
          detail "zaleznosc path=... wskazuje na nieistniejacy katalog (po rozdzieleniu repo czesto poza repo): popraw sciezke, przenies crate do repo albo uzyj wersji z crates.io / git"
        fi
        continue
      fi
      while IFS= read -r mp; do COVERED["$mp"]=1; done \
        < <(grep -o '"manifest_path":"[^"]*"' "$meta" | sed 's/"manifest_path":"//; s/"$//')
      wsroot=$(grep -o '"workspace_root":"[^"]*"' "$meta" | head -n1 | sed 's/"workspace_root":"//; s/"$//')
      [[ -z "$wsroot" ]] && wsroot=$(dirname "$cm")

      # zaleznosci path=... wychodzace poza repo
      while IFS= read -r dp; do
        [[ -z "$dp" ]] && continue
        if ! inside_root "$dp"; then
          subsection "crate $rel"
          fail "zaleznosc path wskazuje poza repo: $dp (po rozdzieleniu repo przestanie dzialac)"; FAILED+=("$CTX: path dep poza repo")
        elif [[ ! -e "$dp" ]]; then
          subsection "crate $rel"
          fail "zaleznosc path nie istnieje: $dp"; FAILED+=("$CTX: path dep nie istnieje")
        fi
      done < <(grep -o '"path":"[^"]*"' "$meta" | sed 's/"path":"//; s/"$//' | sort -u)

      if [[ -z "${WSDONE[$wsroot]:-}" ]]; then WSDONE["$wsroot"]=1; WS_ROOTS+=("$wsroot"); fi
    done

    if ((${#WS_ROOTS[@]} >= 3)); then
      has_root=0; for ws in "${WS_ROOTS[@]}"; do [[ "$ws" == "$ROOT" ]] && has_root=1; done
      ((has_root)) || info "${#WS_ROOTS[@]} osobnych crate'ow bez wspolnego workspace: rozwaz Cargo.toml z [workspace] members=[...] w roocie (jeden Cargo.lock, wspolny target, jedno cargo test)"
    fi
    for ws in ${WS_ROOTS[@]+"${WS_ROOTS[@]}"}; do
      rel=$(rel_of "$ws")
      CTX="rust:$rel"
      subsection "workspace/crate ${rel}"
      lock=()
      if [[ -f "$ws/Cargo.lock" ]]; then lock=(--locked)
      elif has_bin "$ws"; then
        warn "brak Cargo.lock w $rel (to aplikacja/binarka: zacommituj go; wygeneruj: cargo generate-lockfile)"
      else
        info "brak Cargo.lock w $rel (dla samej biblioteki to dopuszczalne; --locked pominiete)"
      fi

      if (cd "$ws" && cargo fmt --version >/dev/null 2>&1); then
        run_step "cargo fmt --check" "$ws" cargo fmt --all -- --check || detail "napraw: (cd $rel && cargo fmt --all)"
      else
        skip "rustfmt niezainstalowany (rustup component add rustfmt)"
      fi
      run_step "cargo check --workspace --all-targets${lock[*]:+ --locked}" "$ws" \
        cargo check --workspace --all-targets ${lock[@]+"${lock[@]}"} \
        || { grep -q 'lock file.*needs to be updated' "$LAST_LOG" && detail "Cargo.lock jest nieaktualny: uruchom cargo update -w albo cargo check bez --locked i zacommituj Cargo.lock"; }
      if ((CLIPPY)); then
        if (cd "$ws" && cargo clippy --version >/dev/null 2>&1); then
          run_step "cargo clippy -D warnings" "$ws" cargo clippy --workspace --all-targets ${lock[@]+"${lock[@]}"} -- -D warnings
        else skip "clippy niezainstalowany (rustup component add clippy)"; fi
      fi
      if ((SKIP_TESTS)); then
        skip "cargo test (--skip-tests)"
      else
        run_step "cargo test --workspace" "$ws" cargo test --workspace ${lock[@]+"${lock[@]}"}
      fi
      if ((VULN)); then
        if (cd "$ws" && cargo audit --version >/dev/null 2>&1); then run_step "cargo audit" "$ws" cargo audit
        else skip "cargo-audit niezainstalowany (cargo install cargo-audit)"; fi
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
    info "brak CMakeLists.txt w repo"
  elif ! have cmake; then
    skip "cmake nie jest zainstalowany: pomijam ${#CMAKES[@]} plikow CMakeLists.txt"
  else
    CXX_FOUND=""
    for c in "${CXX:-}" c++ g++ clang++; do
      [[ -n "$c" ]] && have "$c" && { CXX_FOUND="$c"; break; }
    done
    GEN=()
    have ninja && GEN=(-G Ninja)
    if [[ -z "$CXX_FOUND" ]]; then
      skip "nie znaleziono kompilatora C++ (c++/g++/clang++)"
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
      info "glowne projekty CMake: ${#CM_ROOTS[@]} (kompilator: $CXX_FOUND, generator: ${GEN[1]:-domyslny})"
      for r in "${CM_ROOTS[@]}"; do
        rel=$(rel_of "$r")
        CTX="cmake:$rel"
        subsection "projekt $rel"
        B="$TMPD/cmake.$((++LOGN))"
        if run_step "cmake configure" "$r" cmake -S "$r" -B "$B" -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_CXX_FLAGS="-Wall -Wextra" ${GEN[@]+"${GEN[@]}"}; then
          if run_step "cmake --build (-j$JOBS)" "$r" cmake --build "$B" -j "$JOBS"; then
            nwarn=$(grep -c 'warning:' "$LAST_LOG" || true)
            if ((nwarn > 0)); then
              warn "$nwarn ostrzezen kompilatora (-Wall -Wextra); pierwsze:"
              grep -m5 'warning:' "$LAST_LOG" | sed -E "s#$ROOT/##" | cut -c1-160 | sed 's/^/           | /'
            else
              ok "kompilacja bez ostrzezen (-Wall -Wextra)"
            fi
            if ((SKIP_TESTS)); then
              skip "ctest (--skip-tests)"
            elif have ctest; then
              if run_step "ctest" "$B" ctest --output-on-failure --timeout 120; then
                if grep -q 'No tests were found' "$LAST_LOG"; then
                  warn "ctest nie znalazl zadnych testow (dodaj enable_testing() i add_test() w CMakeLists.txt)"
                fi
              fi
            else
              skip "ctest niezainstalowany"
            fi
          fi
        fi
      done
    fi
  fi
fi

# ==========================================================================
printf '\n%sPodsumowanie:%s %s%d OK%s, %s%d ostrzezen%s, %s%d bledow%s, %d pominietych\n' "$C_BLD" "$C_RST" \
  "$C_GRN" "$N_OK" "$C_RST" "$C_YEL" "$N_WARN" "$C_RST" "$C_RED" "$N_FAIL" "$C_RST" "$N_SKIP"
if ((${#FAILED[@]})); then
  printf '%sDo naprawienia:%s\n' "$C_BLD" "$C_RST"
  printf '  - %s\n' "${FAILED[@]}"
fi
((N_FAIL > 0)) && exit 1
exit 0
