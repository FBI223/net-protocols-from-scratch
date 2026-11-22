# Interop checks with Ruby's Net::HTTP. Usage: ruby client.rb BASE_URL
require "net/http"
require "json"
require "stringio"
require "openssl"

base = URI(ARGV[0])
failed = 0
check = ->(name, ok, detail = "") { puts((ok ? "ok   " : "FAIL ") + name + (ok ? "" : ": #{detail}")); failed += 1 unless ok }

Net::HTTP.start(base.host, base.port, use_ssl: base.scheme == "https", verify_mode: OpenSSL::SSL::VERIFY_NONE) do |h|
  r = h.get("/hello/Ola")
  check.("ruby GET", r.code == "200" && r.body == "Hello, Ola!\n", r.body)

  post = Net::HTTP::Post.new("/echo")
  post.body = "abc"
  check.("ruby POST", h.request(post).body == "abc")

  post = Net::HTTP::Post.new("/echo")
  post["Transfer-Encoding"] = "chunked"
  post.body_stream = StringIO.new("hello chunked")
  check.("ruby POST chunked", h.request(post).body == "hello chunked")

  put = Net::HTTP::Put.new("/kv/rb")
  put.body = { a: 1 }.to_json
  check.("ruby PUT 201", h.request(put).code == "201")
  check.("ruby GET kv", h.get("/kv/rb").body == '{"a":1}')
  check.("ruby DELETE 204", h.delete("/kv/rb").code == "204")
  check.("ruby 404", h.get("/kv/rb").code == "404")
  check.("ruby many requests, one connection", (1..20).all? { |i| h.get("/hello/#{i}").body == "Hello, #{i}!\n" })
end
exit(failed.zero? ? 0 : 1)
