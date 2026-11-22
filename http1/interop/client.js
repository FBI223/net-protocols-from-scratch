// Interop checks with Node's http module and fetch (undici). Usage: node client.js BASE_URL
const http = require("http"), https = require("https"), crypto = require("crypto");
const base = process.argv[2], sha = process.argv[3];
let failed = 0;
const check = (name, ok, detail = "") => { console.log((ok ? "ok   " : "FAIL ") + name + (ok ? "" : ": " + detail)); failed += !ok; };

const mod = base.startsWith("https") ? https : http;
const agent = new mod.Agent({ keepAlive: true, maxSockets: 1, rejectUnauthorized: false });

function req(method, path, body, headers = {}) {
  return new Promise((resolve, reject) => {
    const r = mod.request(base + path, { method, agent, headers }, (res) => {
      const parts = []; res.on("data", (d) => parts.push(d));
      res.on("end", () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(parts) }));
    });
    r.on("error", reject);
    if (body) for (const b of [].concat(body)) r.write(b);  // No Content-Length: Node sends chunked.
    r.end();
  });
}

(async () => {
  let r = await req("GET", "/hello/Ola");
  check("node GET", r.status === 200 && r.body.toString() === "Hello, Ola!\n", r.body.toString());

  r = await req("POST", "/echo", ["he", "llo ", "chunked"]);
  check("node POST chunked", r.body.toString() === "hello chunked", r.body.toString());

  r = await req("POST", "/echo", "abc", { "Content-Length": "3" });
  check("node POST Content-Length", r.body.toString() === "abc");

  r = await req("PUT", "/kv/js", '{"a":1}', { "Content-Type": "application/json" });
  check("node PUT 201", r.status === 201, r.status);
  r = await req("GET", "/kv/js");
  check("node GET kv", r.body.toString() === '{"a":1}', r.body.toString());
  r = await req("DELETE", "/kv/js");
  check("node DELETE 204", r.status === 204);

  r = await req("GET", "/files/big.bin");
  if (r.status === 200 && sha) check("node download", crypto.createHash("sha256").update(r.body).digest("hex") === sha);

  // Several requests at once on a one-socket agent: the client pipelines or queues.
  const rs = await Promise.all([1, 2, 3, 4, 5].map((i) => req("GET", "/hello/p" + i)));
  check("node 5 requests on one socket", rs.every((x, i) => x.body.toString() === `Hello, p${i + 1}!\n`));

  if (!base.startsWith("https")) {  // fetch (undici) checks certificates; use plain HTTP only.
    const f = await fetch(base + "/hello/fetch");
    check("node fetch", (await f.text()) === "Hello, fetch!\n");
    const g = await fetch(base + "/echo", { method: "POST", body: "fetch body" });
    check("node fetch POST", (await g.text()) === "fetch body");
  }
  agent.destroy();
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.log("FAIL exception: " + e); process.exit(1); });
