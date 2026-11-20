#!/usr/bin/env bash
# Downloads the RFC plain-text files for this package from rfc-editor.org into ./rfcs/
#
#   bash fetch-rfcs.sh              # run it from the http1/ folder
#   DRY_RUN=1 bash fetch-rfcs.sh    # only print the URLs
#
# RFC text is not yours to relicense: keep the downloaded files out of git
# or link to them from your notes.
set -euo pipefail

LIST="9112 9110 9293 2119 8174"

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

[ "${DRY_RUN:-0}" = "1" ] || echo "done: $(ls "$OUT" | wc -l) files in $OUT/"
