"""Interop checks with Python's http.client and requests. Usage: client.py BASE_URL"""
import hashlib, http.client, json, os, sys, urllib.parse
for v in ("REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"):  # They would override Session.verify.
    os.environ.pop(v, None)
import requests, urllib3

urllib3.disable_warnings()
base = sys.argv[1]
failed = 0

def check(name, cond, detail=""):
    global failed
    print(("ok   " if cond else "FAIL ") + name + ("" if cond else f": {detail}"))
    failed += not cond

s = requests.Session()  # One session: connections are reused (keep-alive).
s.verify = False

r = s.get(f"{base}/hello/Ola")
check("requests GET", r.status_code == 200 and r.text == "Hello, Ola!\n", (r.status_code, r.text))

r = s.post(f"{base}/echo", data=b"abc")
check("requests POST Content-Length", r.content == b"abc", r.content)

r = s.post(f"{base}/echo", data=(chunk for chunk in [b"he", b"llo ", b"chunked"]))  # Generator: chunked.
check("requests POST chunked", r.content == b"hello chunked", r.content)

r = s.put(f"{base}/kv/py", data=json.dumps({"a": 1}))
check("requests PUT 201", r.status_code == 201 and r.headers.get("Location") == "/kv/py", (r.status_code, r.headers))
r = s.get(f"{base}/kv/py")
check("requests GET kv", r.json() == {"a": 1})
r = s.delete(f"{base}/kv/py")
check("requests DELETE 204", r.status_code == 204 and r.content == b"")
check("requests GET after delete 404", s.get(f"{base}/kv/py").status_code == 404)

r = s.get(f"{base}/files/big.bin")
if r.status_code == 200:
    check("requests download", hashlib.sha256(r.content).hexdigest() == sys.argv[2] if len(sys.argv) > 2 else True)
    r = s.get(f"{base}/files/big.bin", headers={"Range": "bytes=10-19"})
    check("requests Range", r.status_code == 206 and len(r.content) == 10, r.status_code)

r = s.get(f"{base}/stream")
check("requests chunked response", r.text == "".join(f"tick {i}\n" for i in range(1, 6)), r.text)

# Low level: http.client does no magic, so it shows what is really on the wire.
u = urllib.parse.urlparse(base)
cls = http.client.HTTPSConnection if u.scheme == "https" else http.client.HTTPConnection
kw = {"context": __import__("ssl")._create_unverified_context()} if u.scheme == "https" else {}
c = cls(u.hostname, u.port, **kw)
for i in range(3):  # Three requests on one connection.
    c.request("GET", f"/hello/n{i}")
    r = c.getresponse()
    check(f"http.client keep-alive #{i}", r.read() == f"Hello, n{i}!\n".encode())
c.request("GET", "/nope")
r = c.getresponse(); r.read()
check("http.client 404", r.status == 404)
c.request("BREW", "/hello/x")  # Unknown method: 405 (known path) or 404, never a crash.
r = c.getresponse(); r.read()
check("http.client unknown method", r.status in (404, 405, 501), r.status)
sys.exit(1 if failed else 0)
