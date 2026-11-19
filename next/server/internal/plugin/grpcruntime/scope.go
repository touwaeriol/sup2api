package grpcruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// callScopes are the host-client authorities of the Execute, Monitor and
// console HandleHTTP calls in flight on one process, by execution token.
type callScopes struct {
	mu      sync.Mutex
	byToken map[string]*callScope
}

// callScope is the authority of one Execute, Monitor or console HandleHTTP
// call: what its execution token lets the plugin's RelayUpstream and
// ExecuteHTTP calls do. It lives exactly as long as the call.
type callScope struct {
	ctx    context.Context // the call's; ends when it returns
	client core.ExecutionClient
	// account is the account of the call: a fetch may go to the hosts of its
	// own upstream addresses.
	account *pluginv1.Account
	// relay: an Execute call (a Monitor call only fetches); wait: its
	// request allows the waiting form.
	relay, wait bool
	// route: a console HandleHTTP call, whose fetches carry a proxy ticket
	// (CONTRACTS §117); the others' may not.
	route bool

	mu           sync.Mutex
	closed, busy bool
	done         chan struct{} // closed when the active host call ends
	// The rest belongs to the one active host call (busy) and is read by
	// the call's owner after close joined it.
	relayed bool
	parked  *pluginv1.UpstreamRequest // a relay that did not wait
	// evidence: a fetch of this call completed with an HTTP status, without
	// a transport error, while the call was alive. A terminal Monitor result
	// needs it.
	evidence bool
}

// open registers scope under a fresh token.
func (s *callScopes) open(scope *callScope) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byToken == nil {
		s.byToken = map[string]*callScope{}
	}
	s.byToken[token] = scope
	return token, nil
}

// close voids the token and waits for the host call in progress, whose
// context cancel has ended: once close returns, nothing of the client runs.
// The caller cancels the call's context first.
func (s *callScopes) close(token string, scope *callScope) {
	s.mu.Lock()
	delete(s.byToken, token)
	s.mu.Unlock()
	scope.mu.Lock()
	scope.closed = true
	done := scope.done
	scope.mu.Unlock()
	if done != nil {
		<-done
	}
}

// enter starts one host call of the execution token names. The plugin's
// calls of one execution are serial; leave ends the call.
func (h *hostServer) enter(token string) (*callScope, error) {
	if h.p == nil || len(token) != 64 {
		return nil, status.Error(codes.PermissionDenied, "invalid execution token")
	}
	h.p.scopes.mu.Lock()
	sc := h.p.scopes.byToken[token]
	h.p.scopes.mu.Unlock()
	if sc == nil {
		return nil, status.Error(codes.PermissionDenied, "unknown or expired execution token")
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed || sc.ctx.Err() != nil {
		return nil, status.Error(codes.PermissionDenied, "expired execution token")
	}
	if sc.busy {
		return nil, status.Error(codes.FailedPrecondition, "another host call of this execution is active")
	}
	sc.busy = true
	sc.done = make(chan struct{})
	return sc, nil
}

// leave ends the host call enter started.
func (sc *callScope) leave() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.busy = false
	close(sc.done)
}

// hostContext is the context of the client's work for one host call: it
// ends with the Execute/Monitor call and when the plugin gives up the host
// call (rpc), whichever is first.
func (sc *callScope) hostContext(rpc context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(sc.ctx)
	stop := context.AfterFunc(rpc, cancel)
	return ctx, func() { stop(); cancel() }
}

// shallowClone copies the fields of in without copying what they refer to:
// the runtime adds the execution token to a request without changing the
// caller's message or copying its body.
// withScope runs one Execute or Monitor call of the plugin under sc: it
// registers sc under a fresh execution token and hands rpc that token. Once
// rpc returned, whatever the plugin left running ends: a host call still in
// progress is cancelled and joined before the result is read.
func withScope[T any](ctx context.Context, p *proc, sc *callScope, rpc func(ctx context.Context, token string) (T, error)) (T, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc.ctx = ctx
	token, err := p.scopes.open(sc)
	if err != nil {
		var zero T
		return zero, err
	}
	out, err := rpc(ctx, token)
	cancel()
	p.scopes.close(token, sc)
	return out, err
}

func shallowClone[M proto.Message](in M) M {
	out := in.ProtoReflect().New()
	in.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		out.Set(fd, v)
		return true
	})
	return out.Interface().(M)
}
