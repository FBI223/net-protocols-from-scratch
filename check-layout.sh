#!/usr/bin/env bash
#
# check-layout.sh - statyczna kontrola ukladu i higieny repozytorium z protokolami.
# Nie wymaga Go / Rust / CMake i niczego nie modyfikuje.
#
# Uzycie:
#   ./check-layout.sh [opcje] [sciezka-do-repo]     (domyslnie: biezacy katalog)
#
# Opcje:
#   --strict          ostrzezenia tez daja kod wyjscia 1
#   --no-color        wylacz kolory
#   --ignore a,b,c    dodatkowe katalogi najwyzszego poziomu, ktore NIE sa protokolami
#   --stale REGEX     szukaj pozostalosci po starej nazwie/sciezce (mozna podac wiele razy),
#                     np. --stale 'net-protocols-from-scratch/go'
#   -h, --help        ta pomoc
#
# Kod wyjscia: 0 = brak bledow, 1 = sa bledy (lub ostrzezenia przy --strict), 2 = zly argument.
#
# Co sprawdza:
#   1. uklad: protokol-first (arp/go, arp/rust) albo language-first (go/arp), nazwy, duplikaty
#   2. per protokol: kod, README, odwolanie do RFC/3GPP, komenda uruchomienia testow,
#      testy, fuzzing, README przy wielu jezykach (kto jest glowny / interop)
#   3. repo: README, LICENSE, .gitignore, CI, zbedne pliki w roocie
#   4. higiena: sledzone target/build/node_modules, duze pliki, binarki, klucze/sekrety
#   5. manifesty: go.mod / Cargo.toml / CMakeLists.txt, sciezki wychodzace poza repo,
#      brakujace czlonki workspace, add_subdirectory bez CMakeLists.txt
#   6. (opcjonalnie) pozostalosci po starej nazwie modulu / sciezce

set -uo pipefail

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

STRICT=0
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
    --ignore)
      [[ $# -ge 2 ]] || { echo "--ignore wymaga argumentu" >&2; exit 2; }
      EXTRA_IGNORE="$2"; shift ;;
    --stale)
      [[ $# -ge 2 ]] || { echo "--stale wymaga argumentu" >&2; exit 2; }
      STALE+=("$2"); shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "Nieznana opcja: $1 (uzyj --help)" >&2; exit 2 ;;
    *) ROOT_ARG="$1" ;;
  esac
  shift
done

RESOLVED=$(cd "$ROOT_ARG" 2>/dev/null && pwd -P) || { echo "Nie ma takiego katalogu: $ROOT_ARG" >&2; exit 2; }
ROOT="$RESOLVED"

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
  ((n > max)) && out+=", ... (+$((n - max)) wiecej)"
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
# Lista plikow: sledzone + nieignorowane (git) albo find (bez gita)
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
  echo "Brak plikow w $ROOT - zly katalog?" >&2
  exit 2
fi

# --------------------------------------------------------------------------
# Pomocnicze
# --------------------------------------------------------------------------
is_in() { local needle="$1"; shift; local x; for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done; return 1; }
files_in() { awk -v p="$1/" 'index($0, p) == 1' "$ALL"; }
dirs_of() { awk '{ n = split($0, a, "/"); if (n == 1) print "."; else { s = a[1]; for (i = 2; i < n; i++) s = s "/" a[i]; print s } }' | sort -u; }
file_has_i() { grep -qEi -- "$2" "$ROOT/$1" 2>/dev/null; }
any_has() { # regex ; stdin: lista plikow
  local re="$1" f
  while IFS= read -r f; do
    [[ -n "$f" ]] && grep -qE -- "$re" "$ROOT/$f" 2>/dev/null && return 0
  done
  return 1
}
find_readme() {
  awk -v p="$1/" 'index($0, p) == 1 { r = substr($0, length(p) + 1); if (r ~ /^[Rr][Ee][Aa][Dd][Mm][Ee]([.](md|rst|txt|adoc))?$/) { print $0; exit } }' "$ALL"
}
nearest_manifest() { # dir manifest -> wypisuje katalog z manifestem
  local d="$1" m="$2"
  [[ "$d" == "." ]] && d=""
  while :; do
    if [[ -f "$ROOT/${d:+$d/}$m" ]]; then printf '%s' "${d:-.}"; return 0; fi
    [[ -z "$d" ]] && return 1
    if [[ "$d" == */* ]]; then d="${d%/*}"; else d=""; fi
  done
}
resolve_path() { # abs-or-rel-to-dir -> znormalizowana sciezka
  if command -v realpath >/dev/null 2>&1; then realpath -m "$1" 2>/dev/null || printf '%s' "$1"
  else printf '%s' "$1"; fi
}
inside_root() { [[ "$1" == "$ROOT" || "$1" == "$ROOT"/* ]]; }

detect_langs() { # $1 = lista plikow (po nowej linii)
  local fl="$1" out=""
  grep -qE '\.go$' <<<"$fl" && out+="go "
  grep -qE '(\.rs$|(^|/)Cargo\.toml$)' <<<"$fl" && out+="rust "
  if grep -qE '\.(cc|cpp|cxx|hpp|hh|hxx)$' <<<"$fl"; then out+="cpp "
  elif grep -qE '\.(c|h)$' <<<"$fl"; then out+="c "
  fi
  grep -qE '\.py$' <<<"$fl" && out+="python "
  printf '%s' "${out% }"
}

cpp_subset() { grep -E '\.(cc|cpp|cxx|hpp|hh|hxx|c|h)$|(^|/)CMakeLists\.txt$' <<<"$1"; }

has_tests() { # lang fl
  local lang="$1" fl="$2" subset
  case "$lang" in
    go) grep -qE '_test\.go$' <<<"$fl" ;;
    rust)
      grep -qE '(^|/)tests/[^/]+\.rs$' <<<"$fl" && return 0
      subset=$(grep -E '\.rs$' <<<"$fl")
      any_has '#\[(cfg\(test\)|test|tokio::test)\]' <<<"$subset" ;;
    cpp|c)
      subset=$(cpp_subset "$fl")
      grep -qE '(^|/)(tests?|unittests?)/' <<<"$subset" && return 0
      grep -qE '(_tests?|Tests?)\.(cc|cpp|cxx|c)$|(^|/)test_[^/]+\.(cc|cpp|cxx|c)$' <<<"$subset" && return 0
      any_has 'add_test|gtest_discover_tests|catch_discover_tests|TEST\(|TEST_CASE\(|doctest' <<<"$subset" ;;
    python) grep -qE '(^|/)test_[^/]+\.py$|_test\.py$' <<<"$fl" ;;
    *) return 0 ;;
  esac
}

has_fuzz() { # lang fl
  local lang="$1" fl="$2" subset
  case "$lang" in
    go)
      subset=$(grep -E '_test\.go$' <<<"$fl")
      any_has '^func Fuzz[A-Za-z0-9_]*\(' <<<"$subset" ;;
    rust)
      grep -qE '(^|/)fuzz/Cargo\.toml$' <<<"$fl" && return 0
      subset=$(grep -E '(^|/)Cargo\.toml$' <<<"$fl")
      any_has 'proptest|quickcheck|arbitrary|libfuzzer' <<<"$subset" ;;
    cpp|c)
      subset=$(cpp_subset "$fl")
      any_has 'LLVMFuzzerTestOneInput|FuzzedDataProvider' <<<"$subset" ;;
    *) return 1 ;;
  esac
}

# --------------------------------------------------------------------------
printf '%sRepo:%s %s  (%s plikow, %s)\n' "$C_BLD" "$C_RST" "$ROOT" "$(wc -l < "$ALL" | tr -d ' ')" \
  "$( ((IS_GIT)) && echo 'git: licze pliki sledzone i nieignorowane' || echo 'to nie jest repo git: uzywam find' )"

# --------------------------------------------------------------------------
# 1. Wykrycie ukladu
# --------------------------------------------------------------------------
section "1. Uklad katalogow"

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
  warn "uklad mieszany: sa katalogi jezykow (go/, rust/, cpp/ ...) obok katalogow protokolow"
  detail "Wybierz jeden uklad. Dla repo z protokolami polecam protokol-first: arp/go, arp/rust, bgp/cpp ..."
elif ((LANGFIRST)); then
  info "uklad language-first (go/<protokol>, rust/<protokol> ...). Dobry, gdy kazdy protokol ma jedna implementacje."
else
  ok "uklad protokol-first (<protokol>/<jezyk>)"
fi
info "wykryto $TOTAL katalogow z protokolami/projektami"
if ((TOTAL == 0)); then
  warn "nie znalazlem zadnych katalogow z protokolami (sprawdz --ignore albo sciezke repo)"
fi

# nazewnictwo
BAD_NAME=(); LANG_SUFFIX=(); declare -A SEEN=()
for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
  b="${p##*/}"
  [[ "$b" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || BAD_NAME+=("$p")
  key="$b"
  if [[ "$b" =~ -(go|golang|rust|rs|cpp|cxx|c|py|python)$ ]]; then
    LANG_SUFFIX+=("$p")
    key="${b%-*}"
  fi
  [[ "$p" == */* ]] && continue # language-first: ta sama nazwa w kilku jezykach to norma
  SEEN["$key"]+="$p "
done
DUPES=()
for k in "${!SEEN[@]}"; do
  read -r -a _arr <<<"${SEEN[$k]}"
  ((${#_arr[@]} >= 2)) && DUPES+=("${_arr[*]}")
done
check_list warn "nazwy katalogow w kebab-case (male litery, cyfry, myslniki)" \
  "nazwy nie w kebab-case (uzyj malych liter i myslnikow, np. reliable-udp)" "$TOTAL" ${BAD_NAME[@]+"${BAD_NAME[@]}"}
check_list warn "brak nazw z sufiksem jezyka" \
  "nazwy z sufiksem jezyka (np. dns-rust): zrob dns/rust zamiast osobnego katalogu" "$TOTAL" ${LANG_SUFFIX[@]+"${LANG_SUFFIX[@]}"}
check_list warn "brak duplikatow protokolow" \
  "ten sam protokol w kilku katalogach: polacz w <protokol>/<jezyk>" "$TOTAL" ${DUPES[@]+"${DUPES[@]}"}

# --------------------------------------------------------------------------
# 2. Per protokol
# --------------------------------------------------------------------------
section "2. Protokoly: kod, README, testy, fuzzing"

NO_SRC=(); NO_README=(); SHORT_README=(); NO_SPEC=(); NO_RUNHINT=(); NO_TESTS=()
MULTI_NOPRIM=(); MIXED_FLAT=(); NO_FUZZ=()
MX_NAME=(); MX_LANGS=(); MX_README=(); MX_SPEC=(); MX_TEST=(); MX_FUZZ=()

for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
  fl=$(files_in "$p")
  langs=$(detect_langs "$fl")
  if [[ -z "$langs" ]]; then
    NO_SRC+=("$p")
    MX_NAME+=("$p"); MX_LANGS+=("-"); MX_README+=("-"); MX_SPEC+=("-"); MX_TEST+=("-"); MX_FUZZ+=("-")
    continue
  fi

  r_status="no"; s_status="-"
  readme=$(find_readme "$p")
  if [[ -z "$readme" ]]; then
    NO_README+=("$p")
  else
    r_status="ok"
    lines=$(wc -l < "$ROOT/$readme" | tr -d ' ')
    if ((lines < 15)); then SHORT_README+=("$p ($lines linii)"); r_status="short"; fi
    if file_has_i "$readme" 'RFC ?[0-9]+|3GPP|TS ?[0-9]{2}\.[0-9]{3}|IEEE|ITU|ISO/IEC|X\.[0-9]{3}|BCP ?[0-9]+|draft-|OASIS|specification'; then
      s_status="yes"
    else
      s_status="no"; NO_SPEC+=("$p")
    fi
    file_has_i "$readme" 'go test|cargo test|ctest|cmake|make test|make check|pytest|go run|cargo run' || NO_RUNHINT+=("$p")
    nl=$(wc -w <<<"$langs" | tr -d ' ')
    if ((nl >= 2)); then
      file_has_i "$readme" 'primary|reference impl|main impl|interop|glown|peer|cross-test|each other' || MULTI_NOPRIM+=("$p ($langs)")
    fi
  fi

  # mieszane jezyki w jednym plaskim katalogu
  flat=$(awk -v p="$p/" 'index($0, p) == 1 { r = substr($0, length(p) + 1); if (r !~ /\//) print r }' <<<"$fl")
  nflat=0
  grep -qE '\.go$' <<<"$flat" && nflat=$((nflat + 1))
  grep -qE '\.rs$' <<<"$flat" && nflat=$((nflat + 1))
  grep -qE '\.(cc|cpp|cxx|hpp)$' <<<"$flat" && nflat=$((nflat + 1))
  ((nflat >= 2)) && MIXED_FLAT+=("$p")

  # testy / fuzzing per jezyk
  n_l=0; n_t=0; any_fuzz=0
  for l in $langs; do
    n_l=$((n_l + 1))
    if has_tests "$l" "$fl"; then n_t=$((n_t + 1)); else NO_TESTS+=("$p ($l)"); fi
    has_fuzz "$l" "$fl" && any_fuzz=1
  done
  if ((n_t == n_l)); then t_status="ok"; elif ((n_t == 0)); then t_status="none"; else t_status="partial"; fi
  if ((any_fuzz)); then f_status="yes"; else f_status="no"; NO_FUZZ+=("$p"); fi

  MX_NAME+=("$p"); MX_LANGS+=("${langs// /+}"); MX_README+=("$r_status"); MX_SPEC+=("$s_status")
  MX_TEST+=("$t_status"); MX_FUZZ+=("$f_status")
done

check_list warn "kazdy katalog zawiera kod zrodlowy" \
  "katalogow bez kodu zrodlowego (przenies do docs/ albo usun)" "$TOTAL" ${NO_SRC[@]+"${NO_SRC[@]}"}
check_list warn "kazdy protokol ma README" \
  "protokolow bez README (min.: ktory RFC, co dziala, czego brakuje, jak uruchomic testy)" "$TOTAL" ${NO_README[@]+"${NO_README[@]}"}
check_list warn "README protokolow maja sensowna dlugosc" \
  "README ponizej 15 linii" "$TOTAL" ${SHORT_README[@]+"${SHORT_README[@]}"}
check_list warn "README podaja RFC / specyfikacje" \
  "README bez odwolania do RFC/3GPP/IEEE (rekruter chce zobaczyc, wg czego implementujesz)" "$TOTAL" ${NO_SPEC[@]+"${NO_SPEC[@]}"}
check_list warn "README opisuja jak uruchomic testy/build" \
  "README bez komendy uruchomienia (go test / cargo test / ctest ...)" "$TOTAL" ${NO_RUNHINT[@]+"${NO_RUNHINT[@]}"}
check_list warn "protokoly w kilku jezykach opisuja role implementacji" \
  "wiele jezykow, a README nie mowi, ktora wersja jest glowna i jak sie testuja nawzajem (szukam: primary/reference/interop)" "$TOTAL" ${MULTI_NOPRIM[@]+"${MULTI_NOPRIM[@]}"}
check_list warn "jezyki w katalogach protokolow sa rozdzielone" \
  "pliki roznych jezykow w jednym plaskim katalogu: rozdziel na <protokol>/go, <protokol>/rust, <protokol>/cpp" "$TOTAL" ${MIXED_FLAT[@]+"${MIXED_FLAT[@]}"}
check_list warn "kazdy protokol ma testy" \
  "wpisow (protokol, jezyk) bez testow" "$TOTAL" ${NO_TESTS[@]+"${NO_TESTS[@]}"}
check_list info "kazdy protokol ma fuzzing" \
  "protokolow bez fuzzingu (go test -fuzz, cargo fuzz, libFuzzer): parsery pakietow to idealny cel" "$TOTAL" ${NO_FUZZ[@]+"${NO_FUZZ[@]}"}

if ((TOTAL > 0)); then
  printf '\n  %-30s %-12s %-8s %-6s %-8s %-5s\n' "protokol" "jezyki" "README" "RFC" "testy" "fuzz"
  printf '  %-30s %-12s %-8s %-6s %-8s %-5s\n' "------------------------------" "------------" "--------" "------" "--------" "-----"
  for i in "${!MX_NAME[@]}"; do
    printf '  %-30s %-12s %-8s %-6s %-8s %-5s\n' "${MX_NAME[$i]}" "${MX_LANGS[$i]}" "${MX_README[$i]}" \
      "${MX_SPEC[$i]}" "${MX_TEST[$i]}" "${MX_FUZZ[$i]}"
  done
fi

# --------------------------------------------------------------------------
# 3. Poziom glowny repo
# --------------------------------------------------------------------------
section "3. Repozytorium: pliki glowne"

if [[ -f "$ROOT/README.md" ]]; then
  ok "README.md istnieje"
  NOT_IN_README=()
  for p in ${PROTO_DIRS[@]+"${PROTO_DIRS[@]}"}; do
    grep -qiF -- "${p##*/}" "$ROOT/README.md" || NOT_IN_README+=("$p")
  done
  check_list warn "kazdy protokol jest wymieniony w glownym README" \
    "protokolow niewymienionych w glownym README (README ma byc indeksem z linkami)" "$TOTAL" ${NOT_IN_README[@]+"${NOT_IN_README[@]}"}
else
  fail "brak README.md w roocie repo"
fi

if grep -qiE '^(LICENSE|LICENCE|COPYING)(\.(md|txt))?$' "$ALL"; then ok "jest plik LICENSE"; else warn "brak pliku LICENSE (bez licencji nikt nie moze legalnie uzyc kodu)"; fi

if [[ -f "$ROOT/.gitignore" ]]; then
  ok ".gitignore istnieje"
  if grep -qE '(^|/)Cargo\.toml$' "$ALL" && ! grep -qE '^/?(\*\*/)?target/?\*?$' "$ROOT/.gitignore"; then
    warn ".gitignore nie ignoruje target/ (a w repo jest Rust)"
  fi
  if grep -qE '(^|/)CMakeLists\.txt$' "$ALL" && ! grep -qE '^/?(\*\*/)?(build|cmake-build-\*|out)[*/]*$' "$ROOT/.gitignore"; then
    warn ".gitignore nie ignoruje build/ (a w repo jest CMake)"
  fi
else
  warn "brak .gitignore"
fi

if grep -qE '^\.github/workflows/[^/]+\.ya?ml$|^\.gitlab-ci\.yml$' "$ALL"; then
  ok "jest konfiguracja CI"
else
  warn "brak CI (.github/workflows/*.yml): zielony badge z testami to najtanszy sygnal jakosci"
fi

STRAY=$(grep -E '^[^/]+\.(pdf|zip|tar|tgz|gz|7z|rar|docx?|pptx?|xlsx?|log|exe|dmg|iso)$' "$ALL")
if [[ -n "$STRAY" ]]; then
  readarray -t _stray <<<"$STRAY"
  warn "${#_stray[@]} zbednych plikow w roocie (przenies do docs/ albo usun)"
  detail "$(join_items 25 "${_stray[@]}")"
else
  ok "brak zbednych plikow (pdf/zip/log...) w roocie"
fi

# --------------------------------------------------------------------------
# 4. Higiena
# --------------------------------------------------------------------------
section "4. Higiena: smieci, duze pliki, binarki, sekrety"

if ((IS_GIT)); then
  readarray -t HEAVY < <(grep -E '(^|/)(target|build|cmake-build-[^/]+|node_modules|__pycache__)/|(^|/)\.DS_Store$|\.(o|a|so|dylib|obj|exe|class|pyc)$' "$TRACKED")
  if ((${#HEAVY[@]})); then
    fail "${#HEAVY[@]} sledzonych plikow budowania/binarnych (git rm -r --cached <sciezka> i dopisz do .gitignore)"
    detail "$(join_items 15 "${HEAVY[@]}")"
  else
    ok "w gicie nie ma target/ build/ node_modules/ ani plikow obiektowych"
  fi
  readarray -t EDITOR < <(grep -E '(^|/)(\.idea|\.vscode)/' "$TRACKED")
  if ((${#EDITOR[@]})); then
    warn "${#EDITOR[@]} sledzonych plikow edytora (.idea/.vscode): ustaw .gitignore, chyba ze to swiadomy wybor"
  fi
else
  info "to nie repo git: pomijam kontrole sledzonych plikow"
  for d in target build node_modules; do
    [[ -d "$ROOT/$d" ]] && warn "katalog $d/ istnieje na dysku: upewnij sie, ze jest w .gitignore"
  done
fi

# duze pliki
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
  fail "${#BIG_FAIL[@]} plikow > 50 MB (GitHub odrzuca > 100 MB)"; detail "$(join_items 10 "${BIG_FAIL[@]}")"
fi
if ((${#BIG_WARN[@]})); then
  warn "${#BIG_WARN[@]} plikow > 1 MB (pcap, binarki, dane?)"; detail "$(join_items 10 "${BIG_WARN[@]}")"
fi
((${#BIG_FAIL[@]} == 0 && ${#BIG_WARN[@]} == 0)) && ok "brak plikow wiekszych niz 1 MB"

# binarki wykonywalne
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
check_list fail "brak skompilowanych binarek w drzewie" \
  "skompilowanych binarek (ELF/Mach-O/PE): usun i dopisz do .gitignore" "$(wc -l < "$ALL" | tr -d ' ')" ${BINS[@]+"${BINS[@]}"}

# sekrety (nie wypisuje wartosci, tylko plik:linia)
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
    fail "${#S_REAL[@]} podejrzanych kluczy/tokenow poza katalogami testowymi (usun, uniewaznij klucz, wyczysc historie gita)"
    detail "$(join_items 10 "${S_REAL[@]}")"
  fi
  if ((${#S_TEST[@]})); then
    warn "${#S_TEST[@]} kluczy w katalogach testowych: upewnij sie, ze to klucze jednorazowe, wygenerowane tylko do testow"
    detail "$(join_items 10 "${S_TEST[@]}")"
  fi
else
  ok "nie znalazlem kluczy prywatnych ani tokenow (AWS/GitHub/Slack)"
fi
info "hasla w kodzie protokolow (np. shared secret w RADIUS) sa tu pomijane: tylko wzorce znanych tokenow"

# --------------------------------------------------------------------------
# 5. Manifesty i zaleznosci (statycznie)
# --------------------------------------------------------------------------
section "5. Manifesty i zaleznosci (bez uruchamiania toolchaina)"

# --- Go
GO_FILES=$(grep -E '\.go$' "$ALL" | grep -vE '(^|/)(testdata|vendor)/' || true)
if [[ -n "$GO_FILES" ]]; then
  ORPHAN_GO=()
  while IFS= read -r d; do
    nearest_manifest "$d" go.mod >/dev/null || ORPHAN_GO+=("$d")
  done < <(dirs_of <<<"$GO_FILES")
  check_list fail "kazdy katalog z .go nalezy do modulu (go.mod)" \
    "katalogow z .go bez go.mod w gore drzewa (po rozdzieleniu repo zwykle brakuje go.mod: go mod init ...)" \
    "$(dirs_of <<<"$GO_FILES" | wc -l | tr -d ' ')" ${ORPHAN_GO[@]+"${ORPHAN_GO[@]}"}

  readarray -t GOMODS < <(grep -E '(^|/)go\.mod$' "$ALL" | grep -vE '(^|/)(testdata|vendor)/')
  declare -A MODSEEN=()
  BAD_MODPATH=(); NO_GODIR=(); BAD_REPLACE=(); NO_GOSUM=()
  for gm in ${GOMODS[@]+"${GOMODS[@]}"}; do
    mdir=$(dirname "$gm")
    mpath=$(awk '/^module[ \t]/ { gsub(/"/, "", $2); print $2; exit }' "$ROOT/$gm")
    info "modul Go: ${mpath:-<brak>} (${mdir})"
    [[ -z "$mpath" ]] && { BAD_MODPATH+=("$gm (brak linii module)"); continue; }
    [[ "${mpath%%/*}" == *.* ]] || BAD_MODPATH+=("$mpath")
    MODSEEN["$mpath"]+="$gm "
    grep -qE '^go [0-9]' "$ROOT/$gm" || NO_GODIR+=("$gm")
    if grep -qE '^[[:space:]]*(require|[a-zA-Z0-9._~/-]+[[:space:]]+v[0-9])' "$ROOT/$gm"; then
      [[ -f "$ROOT/$mdir/go.sum" ]] || NO_GOSUM+=("$gm")
    fi
    while IFS= read -r line; do
      tgt=$(sed -E 's/.*=>[[:space:]]*([^[:space:]]+).*/\1/' <<<"$line")
      abs=$(resolve_path "$ROOT/$mdir/$tgt")
      if ! inside_root "$abs"; then BAD_REPLACE+=("$gm: => $tgt (poza repo)")
      elif [[ ! -d "$abs" ]]; then BAD_REPLACE+=("$gm: => $tgt (nie istnieje)"); fi
    done < <(grep -E '=>[[:space:]]*\.{1,2}/' "$ROOT/$gm" || true)
  done
  check_list warn "sciezki modulow wygladaja jak host/uzytkownik/repo" \
    "sciezek modulu bez domeny (go get nie zadziala; uzyj np. github.com/FBI223/nazwa-repo)" "${#GOMODS[@]}" ${BAD_MODPATH[@]+"${BAD_MODPATH[@]}"}
  check_list warn "go.mod maja dyrektywe go" "go.mod bez dyrektywy 'go 1.xx'" "${#GOMODS[@]}" ${NO_GODIR[@]+"${NO_GODIR[@]}"}
  check_list fail "go.mod z zaleznosciami maja go.sum" "go.mod z zaleznosciami, ale bez go.sum (go mod tidy)" "${#GOMODS[@]}" ${NO_GOSUM[@]+"${NO_GOSUM[@]}"}
  check_list fail "brak lokalnych replace wychodzacych poza repo" \
    "lokalne replace w go.mod, ktore po rozdzieleniu repo przestana dzialac" "${#GOMODS[@]}" ${BAD_REPLACE[@]+"${BAD_REPLACE[@]}"}
  DUP_MODS=()
  for k in "${!MODSEEN[@]}"; do
    read -r -a _m <<<"${MODSEEN[$k]}"
    ((${#_m[@]} >= 2)) && DUP_MODS+=("$k: ${_m[*]}")
  done
  check_list fail "kazdy modul Go ma unikalna sciezke" "ta sama sciezka modulu w kilku go.mod" "${#GOMODS[@]}" ${DUP_MODS[@]+"${DUP_MODS[@]}"}
else
  info "brak kodu Go"
fi

# --- Rust
readarray -t CARGOS < <(grep -E '(^|/)Cargo\.toml$' "$ALL" | grep -vE '(^|/)(testdata|target)/')
if ((${#CARGOS[@]})); then
  RS_BAD_PATH=(); RS_BAD_MEMBER=(); RS_NO_LOCK=()
  for cm in "${CARGOS[@]}"; do
    cdir=$(dirname "$cm")
    # path = "..." w zaleznosciach (bez ../ poza repo)
    while IFS= read -r line; do
      tgt=$(sed -E 's/.*path[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/' <<<"$line")
      abs=$(resolve_path "$ROOT/$cdir/$tgt")
      if ! inside_root "$abs"; then RS_BAD_PATH+=("$cm: path=\"$tgt\" (poza repo)")
      elif [[ ! -e "$abs" ]]; then RS_BAD_PATH+=("$cm: path=\"$tgt\" (nie istnieje)"); fi
    done < <(grep -E 'path[[:space:]]*=[[:space:]]*"[^"]+"' "$ROOT/$cm" | grep -vE '^[[:space:]]*#' || true)
    # czlonkowie workspace
    if grep -qE '^\[workspace\]' "$ROOT/$cm"; then
      while IFS= read -r m; do
        [[ -z "$m" ]] && continue
        matched=0
        for g in "$ROOT/$cdir"/$m; do
          if [[ -f "$g/Cargo.toml" ]]; then matched=1; fi
        done
        ((matched)) || RS_BAD_MEMBER+=("$cm: member \"$m\" (brak katalogu z Cargo.toml)")
      done < <(awk '
        /^\[workspace\]/ { inws = 1; next }
        /^\[/ && !/^\[workspace/ { inws = 0 }
        inws && /members[[:space:]]*=/ { inm = 1 }
        inm { line = $0
              while (match(line, /"[^"]+"/)) { print substr(line, RSTART + 1, RLENGTH - 2); line = substr(line, RSTART + RLENGTH) }
              if ($0 ~ /\]/) inm = 0 }' "$ROOT/$cm")
      nearest_manifest "$cdir" Cargo.lock >/dev/null || RS_NO_LOCK+=("$cdir (workspace)")
    elif [[ -f "$ROOT/$cdir/src/main.rs" ]] || grep -qE '^\[\[bin\]\]' "$ROOT/$cm"; then
      # binarka: Cargo.lock w katalogu crate'a albo w katalogu workspace powyzej
      nearest_manifest "$cdir" Cargo.lock >/dev/null || RS_NO_LOCK+=("$cdir (binarka)")
    fi
  done
  check_list fail "sciezkowe zaleznosci Cargo wskazuja na istniejace katalogi w repo" \
    "zaleznosci path=... poza repo albo nieistniejace (po rozdzieleniu repo przestana dzialac)" "${#CARGOS[@]}" ${RS_BAD_PATH[@]+"${RS_BAD_PATH[@]}"}
  check_list fail "czlonkowie workspace istnieja" "czlonkowie workspace bez katalogu" "${#CARGOS[@]}" ${RS_BAD_MEMBER[@]+"${RS_BAD_MEMBER[@]}"}
  check_list warn "workspace i binarki maja Cargo.lock" "brak Cargo.lock (dla aplikacji/workspace trzymaj go w repo)" "${#CARGOS[@]}" ${RS_NO_LOCK[@]+"${RS_NO_LOCK[@]}"}
else
  info "brak kodu Rust"
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
      [[ -f "$ROOT/$target/CMakeLists.txt" ]] || CM_MISSING+=("$cm: add_subdirectory($sub) bez CMakeLists.txt")
    done < <(grep -oiE 'add_subdirectory[[:space:]]*\([[:space:]]*"?[^) "]+' <<<"$stripped" | sed -E 's/.*\([[:space:]]*"?//')
  done
  for cm in "${CMAKES[@]}"; do
    cdir=$(dirname "$cm")
    [[ "$cdir" == "." ]] && continue
    parent=$(dirname "$cdir")
    [[ -f "$ROOT/$parent/CMakeLists.txt" || ( "$parent" == "." && -f "$ROOT/CMakeLists.txt" ) ]] || continue
    rel="${cdir#"$parent"/}"; [[ "$parent" == "." ]] && rel="$cdir"
    case " ${SUBS[$parent]:-} " in *" $rel "*) ;; *) CM_ORPHAN+=("$cm (nie ma add_subdirectory($rel) w $parent/CMakeLists.txt)") ;; esac
  done
  check_list fail "add_subdirectory wskazuje na istniejace katalogi" "add_subdirectory do katalogow bez CMakeLists.txt" "${#CMAKES[@]}" ${CM_MISSING[@]+"${CM_MISSING[@]}"}
  check_list warn "kazdy zagniezdzony CMakeLists jest wlaczony przez add_subdirectory" "zagniezdzone CMakeLists nieuzywane przez rodzica" "${#CMAKES[@]}" ${CM_ORPHAN[@]+"${CM_ORPHAN[@]}"}
  check_list warn "projekty CMake maja cmake_minimum_required" "project() bez cmake_minimum_required" "${#CMAKES[@]}" ${CM_NOMIN[@]+"${CM_NOMIN[@]}"}
else
  if grep -qE '\.(cc|cpp|cxx|hpp)$' "$ALL"; then
    warn "sa pliki C++, ale nigdzie nie ma CMakeLists.txt"
  else
    info "brak kodu C++"
  fi
fi

# --------------------------------------------------------------------------
# 6. Pozostalosci po starej nazwie
# --------------------------------------------------------------------------
if ((${#STALE[@]})); then
  section "6. Pozostalosci po starej nazwie / sciezce"
  for re in "${STALE[@]}"; do
    HITS=$(cd "$ROOT" && tr '\n' '\0' < "$ALL" | xargs -0 grep -InE -- "$re" 2>/dev/null | grep -vE 'check-(layout|build)\.sh' || true)
    if [[ -z "$HITS" ]]; then
      ok "brak wystapien: $re"
    else
      cnt=$(wc -l <<<"$HITS" | tr -d ' ')
      warn "$cnt wystapien: $re (po rozdzieleniu repo to zwykle stare importy / linki w README)"
      head -n 12 <<<"$HITS" | cut -c1-140 | sed 's/^/           /'
    fi
  done
fi

# --------------------------------------------------------------------------
printf '\n%sPodsumowanie:%s %s%d OK%s, %s%d ostrzezen%s, %s%d bledow%s, %d wskazowek\n' "$C_BLD" "$C_RST" \
  "$C_GRN" "$N_OK" "$C_RST" "$C_YEL" "$N_WARN" "$C_RST" "$C_RED" "$N_FAIL" "$C_RST" "$N_INFO"
if ((N_FAIL > 0)); then exit 1; fi
if ((STRICT && N_WARN > 0)); then exit 1; fi
exit 0
