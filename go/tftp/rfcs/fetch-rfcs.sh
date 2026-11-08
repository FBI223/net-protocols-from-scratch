#!/usr/bin/env bash
# Downloads RFC plain-text files from rfc-editor.org into ./rfcs/
#
#   bash fetch-rfcs.sh          # TFTP set only (default)
#   bash fetch-rfcs.sh all      # every RFC listed in protocols-from-scratch/README.md
#   DRY_RUN=1 bash fetch-rfcs.sh all   # only print the URLs
#
# Put the result in docs/rfc-notes/ or keep it outside git (RFC text is not
# yours to relicense; link to it from your notes instead of committing it).
set -euo pipefail

TFTP="1350 2347 2348 2349 1123 768 2119"
ALL="$TFTP 1034 1035 6891 9293 9112 6455 2131 2132 826 791 792 5905 8489 8446 9000 2784 7348 4271 7854 2328 3416 7011 5424 2865 2866 5176 3261 8866 3550"

case "${1:-tftp}" in
  tftp) LIST="$TFTP" ;;
  all)  LIST="$ALL" ;;
  *) echo "usage: $0 [tftp|all]" >&2; exit 2 ;;
esac

OUT="${OUT:-rfcs}"
mkdir -p "$OUT"

for n in $(printf '%s\n' $LIST | sort -un); do
  url="https://www.rfc-editor.org/rfc/rfc${n}.txt"
  if [ "${DRY_RUN:-0}" = "1" ]; then
    echo "$url"
    continue
  fi
  if [ -s "$OUT/rfc${n}.txt" ]; then
    echo "skip    rfc${n}.txt (already there)"
    continue
  fi
  echo "fetch   rfc${n}.txt"
  curl -fsSL --retry 2 -o "$OUT/rfc${n}.txt" "$url"
  sleep 1   # be polite to the server
done

echo "done: $(ls "$OUT" | wc -l) files in $OUT/"
