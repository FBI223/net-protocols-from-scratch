#!/usr/bin/env bash
# Interoperability check: talks to cmd/httpd with HTTP clients written by
# other people (curl, wget, Python, Node, Ruby, openssl, nc). Clients that are
# not installed are skipped. Run from anywhere:  interop/run.sh
set -u
cd "$(dirname "$0")/.."
WORK=$(mktemp -d); trap 'kill $PIDS 2>/dev/null; rm -rf "$WORK"' EXIT
PIDS=""; PASS=0; FAIL=0; SKIP=0

go build -o "$WORK/httpd" ./cmd/httpd || exit 1
mkdir "$WORK/site"; head -c 300000 /dev/urandom > "$WORK/site/big.bin"
SHA=$( (sha256sum "$WORK/site/big.bin" 2>/dev/null || shasum -a 256 "$WORK/site/big.bin") | cut -d' ' -f1)

have() { command -v "$1" >/dev/null 2>&1; }
report() { # name, exit status
  if [ "$2" -eq 0 ]; then echo "  PASS  $1"; PASS=$((PASS+1)); else echo "  FAIL  $1"; FAIL=$((FAIL+1)); fi
}
skip() { echo "  skip  $1 (not installed)"; SKIP=$((SKIP+1)); }
run() { # name, command...
  local name=$1; shift; local out; out=$("$@" 2>&1); local st=$?
  [ $st -ne 0 ] && echo "$out" | sed 's/^/        /'
  [ $st -eq 0 ] && echo "$out" | grep -q '^FAIL' && st=1
  report "$name" $st
}
start() { "$WORK/httpd" "$@" >"$WORK/log.$$" 2>&1 & PIDS="$PIDS $!"; LAST=$!; sleep 0.6; }
stop() { kill -INT $LAST 2>/dev/null; wait $LAST 2>/dev/null; }

curl_checks() { # base, extra curl args...
  local b=$1; shift
  [ "$(curl -s "$@" "$b/hello/Ola")" = "Hello, Ola!" ] || { echo "FAIL GET"; return 1; }
  [ "$(printf abc | curl -s "$@" --data-binary @- "$b/echo")" = abc ] || { echo "FAIL POST"; return 1; }
  [ "$(printf 'x y z' | curl -s "$@" -H 'Transfer-Encoding: chunked' --data-binary @- "$b/echo")" = "x y z" ] || { echo "FAIL chunked POST"; return 1; }
  [ "$(curl -s "$@" -o /dev/null -w '%{http_code}' -X PUT -d '{"k":1}' "$b/kv/c")" = 201 ] || { echo "FAIL PUT"; return 1; }
  [ "$(curl -s "$@" "$b/kv/c")" = '{"k":1}' ] || { echo "FAIL kv GET"; return 1; }
  [ "$(curl -s "$@" -o /dev/null -w '%{http_code}' -X DELETE "$b/kv/c")" = 204 ] || { echo "FAIL DELETE"; return 1; }
  [ "$(curl -s "$@" "$b/files/big.bin" | (sha256sum 2>/dev/null || shasum -a 256) | cut -d' ' -f1)" = "$SHA" ] || { echo "FAIL download"; return 1; }
  [ "$(curl -s "$@" -r 5-14 "$b/files/big.bin" | wc -c | tr -d ' ')" = 10 ] || { echo "FAIL range"; return 1; }
  # Two URLs in one invocation: the second must reuse the connection.
  [ "$(curl -s "$@" -o /dev/null -o /dev/null -w '%{num_connects} ' "$b/hello/1" "$b/hello/2")" = "1 0 " ] || { echo "FAIL keep-alive"; return 1; }
  [ "$(curl -s "$@" -o /dev/null -w '%{http_code}' "$b/nope")" = 404 ] || { echo "FAIL 404"; return 1; }
  [ "$(curl -s "$@" -o /dev/null -w '%{http_code}' --path-as-is "$b/files/../../etc/passwd")" = 400 ] || { echo "FAIL traversal"; return 1; }
  [ "$(curl -s "$@" --http1.0 "$b/hello/old")" = "Hello, old!" ] || { echo "FAIL HTTP/1.0"; return 1; }
}

raw_checks() { # host port: bytes typed by hand with nc
  local out
  out=$(printf 'GET /hello/nc HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n' | nc -w 3 "$1" "$2")
  echo "$out" | head -1 | grep -q '^HTTP/1.1 200 OK' || { echo "FAIL nc GET"; return 1; }
  echo "$out" | grep -q 'Hello, nc!' || { echo "FAIL nc body"; return 1; }
  out=$(printf 'GET /hello/x HTTP/1.1\r\n\r\n' | nc -w 3 "$1" "$2")   # no Host header
  echo "$out" | head -1 | grep -q ' 400 ' || { echo "FAIL nc missing Host"; return 1; }
  out=$(printf 'POST /echo HTTP/1.1\r\nHost: h\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n' | nc -w 3 "$1" "$2")
  echo "$out" | head -1 | grep -q ' 400 ' || { echo "FAIL nc CL+TE not rejected"; return 1; }
}

echo "== TCP (127.0.0.1:18090)"
start -addr 127.0.0.1:18090 -root "$WORK/site"
B=http://127.0.0.1:18090
have curl    && run curl      curl_checks $B              || skip curl
have wget    && run wget      bash -c "[ \"\$(wget -qO- $B/hello/wg)\" = 'Hello, wg!' ] && wget -q --post-data=abc -O- $B/echo | grep -q abc" || skip wget
have nc      && run "nc (raw bytes)" raw_checks 127.0.0.1 18090 || skip nc
have python3 && python3 -c 'import requests' 2>/dev/null && run "python requests + http.client" python3 interop/client.py $B $SHA || skip "python requests"
have node    && run "node http + fetch" node interop/client.js $B $SHA || skip node
have ruby    && run "ruby Net::HTTP" ruby interop/client.rb $B || skip ruby
stop

echo "== Unix socket"
start -unix "$WORK/h.sock" -root "$WORK/site"
have curl && run "curl --unix-socket" curl_checks http://localhost --unix-socket "$WORK/h.sock" || skip curl
stop

echo "== TLS (self-signed, 127.0.0.1:18091)"
start -addr 127.0.0.1:18091 -tls-self-signed -root "$WORK/site"
B=https://127.0.0.1:18091
have curl    && run "curl -k"  curl_checks $B -k || skip curl
have openssl && run "openssl s_client (raw bytes over TLS)" bash -c "printf 'GET /hello/tls HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n' | openssl s_client -quiet -connect 127.0.0.1:18091 2>/dev/null | grep -q 'Hello, tls!'" || skip openssl
have python3 && python3 -c 'import requests' 2>/dev/null && run "python requests (TLS)" python3 interop/client.py $B $SHA || skip "python requests"
have node    && run "node https" env NODE_TLS_REJECT_UNAUTHORIZED=0 node interop/client.js $B $SHA || skip node
have ruby    && run "ruby Net::HTTP (TLS)" ruby interop/client.rb $B || skip ruby
stop

echo
echo "passed $PASS, failed $FAIL, skipped $SKIP"
[ $FAIL -eq 0 ]
