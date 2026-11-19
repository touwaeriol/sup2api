// Package pgproxy logs in to PostgreSQL on behalf of a client that has no
// credentials, then relays the wire protocol unchanged. The core uses it for
// plugin database connections (CONTRACTS §115): a plugin connects through
// the egress tunnel to a fixed virtual address, the core terminates the
// start-up, opens the server connection with its own configuration (TLS and
// password authentication included) and the plugin's schema as search_path,
// and from then on copies bytes both ways. No database credential reaches the
// client.
//
// It lives under runtime-contract/gormpg because that is the only place that
// may import pgx.
package pgproxy

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
)

// Proxy opens server connections with one configuration and remembers the
// sessions it relays, so that a client's cancel request reaches the server.
type Proxy struct {
	cfg *pgconn.Config

	mu       sync.Mutex
	sessions map[uint32]*session // by server process id
}

type session struct {
	client    string // the client identity Serve was given
	secretKey []byte
	addr      net.Addr    // the server's address, where cancel requests go
	tlsConfig *tls.Config // nil when the server connection is not TLS
}

// New parses dsn (URL or key=value, as the core's own pool does) once.
func New(dsn string) (*Proxy, error) {
	cfg, err := gormpg.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return &Proxy{cfg: &cfg.Config, sessions: map[uint32]*session{}}, nil
}

// Session is what Serve sets on the server connection for one client.
type Session struct {
	// Client identifies the client (the plugin key). A cancel request is
	// forwarded only for a session of the same client.
	Client string
	// Params override run-time parameters of the server session, after the
	// client's own start-up parameters (e.g. search_path, application_name).
	Params map[string]string
}

// ErrorMessage is what a client is told when the server connection cannot be
// opened; the reason is in the error Serve returns, not sent to the client.
const ErrorMessage = "the host could not open the database connection"

// Serve runs one client connection to the end and closes it. The client's
// SSL and GSS encryption requests are declined (the client is local, behind
// the tunnel); its user, database and options are ignored; a cancel request
// is forwarded to the server. The returned error describes why the
// connection could not be set up; relay errors after that are not errors.
func (p *Proxy) Serve(ctx context.Context, client net.Conn, s Session) error {
	defer client.Close()
	be := pgproto3.NewBackend(client, client)
	var start *pgproto3.StartupMessage
	for start == nil {
		msg, err := be.ReceiveStartupMessage()
		if err != nil {
			return fmt.Errorf("pgproxy: read start-up: %w", err)
		}
		switch m := msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			if _, err := client.Write([]byte{'N'}); err != nil {
				return err
			}
		case *pgproto3.CancelRequest:
			return p.cancel(ctx, s.Client, m)
		case *pgproto3.StartupMessage:
			start = m
		default:
			return fmt.Errorf("pgproxy: unexpected start-up message %T", msg)
		}
	}

	server, err := p.connect(ctx, start, s)
	if err != nil {
		fail := &pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "08006", Message: ErrorMessage}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// The server's own refusal (too many connections, the database
			// shutting down) carries no credential.
			fail.Code, fail.Message = pgErr.Code, pgErr.Message
		}
		be.Send(fail)
		_ = be.Flush()
		return err
	}
	defer server.Conn.Close()

	be.Send(&pgproto3.AuthenticationOk{})
	for k, v := range server.ParameterStatuses {
		be.Send(&pgproto3.ParameterStatus{Name: k, Value: v})
	}
	be.Send(&pgproto3.BackendKeyData{ProcessID: server.PID, SecretKey: server.SecretKey})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: server.TxStatus})
	if err := be.Flush(); err != nil {
		return nil
	}

	p.mu.Lock()
	p.sessions[server.PID] = &session{client: s.Client, secretKey: server.SecretKey, addr: server.Conn.RemoteAddr(), tlsConfig: server.TLSConfig}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.sessions, server.PID)
		p.mu.Unlock()
	}()

	relay(client, server.Conn)
	return nil
}

// connect opens the server connection with the proxy's configuration, the
// client's protocol version and run-time parameters, and s.Params.
func (p *Proxy) connect(ctx context.Context, start *pgproto3.StartupMessage, s Session) (*pgconn.HijackedConn, error) {
	cfg := p.cfg.Copy()
	for k, v := range start.Parameters {
		switch {
		case k == "user", k == "database", k == "options", k == "replication", strings.HasPrefix(k, "_pq_."):
		default:
			cfg.RuntimeParams[k] = v
		}
	}
	for k, v := range s.Params {
		cfg.RuntimeParams[k] = v
	}
	version := "3.0"
	if start.ProtocolVersion == pgproto3.ProtocolVersion32 {
		version = "3.2"
	}
	cfg.MinProtocolVersion, cfg.MaxProtocolVersion = version, version
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := conn.SyncConn(ctx); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}
	return conn.Hijack()
}

// relay copies both ways until either side ends, then closes both.
func relay(client, server net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		_ = client.Close()
		_ = server.Close()
		done <- struct{}{}
	}
	go cp(server, client)
	go cp(client, server)
	<-done
	<-done
}

// cancel forwards a client's cancel request for one of its own sessions to
// the server, encrypted like the session's connection (as pgconn does).
// An unknown or foreign session is ignored, as the server ignores a wrong key.
func (p *Proxy) cancel(ctx context.Context, client string, req *pgproto3.CancelRequest) error {
	p.mu.Lock()
	s := p.sessions[req.ProcessID]
	p.mu.Unlock()
	if s == nil || s.client != client || subtle.ConstantTimeCompare(s.secretKey, req.SecretKey) != 1 {
		return nil
	}
	conn, err := p.cfg.DialFunc(ctx, s.addr.Network(), s.addr.String())
	if err != nil {
		return fmt.Errorf("pgproxy: cancel: %w", err)
	}
	defer conn.Close()
	if s.tlsConfig != nil {
		if p.cfg.SSLNegotiation != "direct" {
			req, err := (&pgproto3.SSLRequest{}).Encode(nil)
			if err != nil {
				return err
			}
			if _, err := conn.Write(req); err != nil {
				return fmt.Errorf("pgproxy: cancel: %w", err)
			}
			b := make([]byte, 1)
			if _, err := io.ReadFull(conn, b); err != nil {
				return fmt.Errorf("pgproxy: cancel: %w", err)
			}
			if b[0] != 'S' {
				return errors.New("pgproxy: cancel: the server refused TLS")
			}
		}
		conn = tls.Client(conn, s.tlsConfig)
	}
	msg, err := (&pgproto3.CancelRequest{ProcessID: req.ProcessID, SecretKey: s.secretKey}).Encode(nil)
	if err != nil {
		return err
	}
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("pgproxy: cancel: %w", err)
	}
	// The server closes the connection once it has read the request.
	_, _ = io.Copy(io.Discard, conn)
	return nil
}
