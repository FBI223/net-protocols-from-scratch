// Server: accept loop on a net.Listener, one goroutine per connection, limit on
// simultaneous connections, graceful shutdown with a context, and a way for a
// handler to take over the connection (needed later by websocket/).

package http1
