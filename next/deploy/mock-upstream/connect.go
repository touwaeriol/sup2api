package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
)

// HTTPS through a forward proxy: a client whose proxy is the mock (an
// account's proxy, or the proxy of an authorization) sends CONNECT
// host:443 for an https address. The mock answers 200 and serves the tunnel
// itself, with the certificates and handler of its TLS listener (tls.go),
// whatever host the client named: every vendor host it knows is the mock.
// Requests inside a tunnel are recorded like those on the TLS listener, with
// proxied=true. Plain-http proxying needs nothing of this: the request
// arrives with an absolute URI (record).

// tunneledKey marks the context of a connection that came through CONNECT.
type tunneledKey struct{}

func tunneled(r *http.Request) bool { return r.Context().Value(tunneledKey{}) != nil }

// tunnelListener hands the hijacked CONNECT connections to the tunnel
// server (serveTLS); nil on a mock without a TLS listener.
type tunnelListener struct {
	conns chan net.Conn
	once  sync.Once
	done  chan struct{}
}

func newTunnelListener() *tunnelListener {
	return &tunnelListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *tunnelListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *tunnelListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *tunnelListener) Addr() net.Addr { return tunnelAddr{} }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "connect" }
func (tunnelAddr) String() string  { return "connect-tunnel" }

// tunnelServer serves h on the tunnels; its connections are marked tunneled.
func tunnelServer(h http.Handler, cfg *tls.Config) *http.Server {
	return &http.Server{Handler: h, TLSConfig: cfg,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, tunneledKey{}, true)
		}}
}

// connect answers a CONNECT and hands the connection to the tunnel server.
func (s *server) connect(w http.ResponseWriter, _ *http.Request) {
	if s.tunnels == nil {
		http.Error(w, "mock: no TLS listener to tunnel to (MOCK_TLS_ADDR)", http.StatusBadGateway)
		return
	}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "mock: hijack: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// The client sends nothing more until it reads the 200.
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		return
	}
	select {
	case s.tunnels.conns <- conn:
	case <-s.tunnels.done:
		conn.Close()
	}
}
