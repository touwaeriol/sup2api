package pluginsdk

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

func TestRouterPathParams(t *testing.T) {
	r := NewRouter()
	var capturedID string
	r.Handle("GET", "/models/:id", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		capturedID = Param(req, "id")
		return DataResponse(map[string]string{"id": capturedID}), nil
	})

	resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{
		Method: "GET",
		Path:   "/models/gpt-4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetStatus() != 200 {
		t.Fatalf("expected 200, got %d", resp.GetStatus())
	}
	if capturedID != "gpt-4" {
		t.Fatalf("expected id=gpt-4, got %q", capturedID)
	}
}

func TestRouterExactMatchBeforePattern(t *testing.T) {
	r := NewRouter()
	r.Handle("GET", "/models/:id", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("pattern"), nil
	})
	r.Handle("GET", "/models/list", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("exact"), nil
	})

	resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{
		Method: "GET",
		Path:   "/models/list",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.GetBody()) != `{"data":"exact"}` {
		t.Fatalf("exact match should win, got: %s", resp.GetBody())
	}
}

func TestRouterMultipleParams(t *testing.T) {
	r := NewRouter()
	var capturedUser, capturedRepo string
	r.Handle("GET", "/users/:user/repos/:repo", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		capturedUser = Param(req, "user")
		capturedRepo = Param(req, "repo")
		return DataResponse(nil), nil
	})

	_, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{
		Method: "GET",
		Path:   "/users/alice/repos/project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedUser != "alice" || capturedRepo != "project" {
		t.Fatalf("expected alice/project, got %s/%s", capturedUser, capturedRepo)
	}
}

// TestRouterPatternPriority pins the choice between two patterns that both
// match: the one with more literal segments wins, and a tie is broken by the
// pattern text. Before the routes were compiled, the winner was whichever the
// map iteration reached first, i.e. it could differ between runs.
func TestRouterPatternPriority(t *testing.T) {
	newRouter := func() *Router {
		r := NewRouter()
		r.Handle("GET", "/a/:id", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
			return DataResponse("param"), nil
		})
		r.Handle("GET", "/a/b", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
			return DataResponse("literal"), nil
		})
		return r
	}
	// Both /a/b (exact) and /a/:id (param) can serve /a/b; the literal must win
	// regardless of registration order, so run it several times.
	for i := 0; i < 20; i++ {
		r := newRouter()
		resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", Path: "/a/b"})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(resp.GetBody()); got != `{"data":"literal"}` {
			t.Fatalf("iteration %d: %s", i, got)
		}
		// /a/c still reaches the parameter route, and the parameter is filled.
		req := &pluginv1.HTTPRequest{Method: "GET", Path: "/a/c"}
		resp, err = r.HandleHTTP(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(resp.GetBody()); got != `{"data":"param"}` {
			t.Fatalf("iteration %d: %s", i, got)
		}
		if req.GetPathParams()["id"] != "c" {
			t.Fatalf("path params = %v", req.GetPathParams())
		}
	}
}

// TestRouterOnlyTheMatchingMethod: a pattern of another method never answers.
func TestRouterOnlyTheMatchingMethod(t *testing.T) {
	r := NewRouter()
	r.Handle("POST", "/rules/:id", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("post"), nil
	})
	resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", Path: "/rules/1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != 404 {
		t.Fatalf("status = %d, want 404", resp.GetStatus())
	}
}

// TestRouterReregisterReplaces: Handle on the same method and path twice keeps
// the second handler (the map-backed router replaced it) and does not leave a
// duplicate entry behind that a later re-sort could reorder.
func TestRouterReregisterReplaces(t *testing.T) {
	r := NewRouter()
	r.Handle("GET", "/rules/:id", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("first"), nil
	})
	r.Handle("GET", "/rules/:id", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("second"), nil
	})
	resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", Path: "/rules/1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.GetBody()); got != `{"data":"second"}` {
		t.Fatalf("body = %s, want the second handler", got)
	}
	if n := len(r.byMethod["GET"]); n != 1 {
		t.Fatalf("registered %d routes, want 1", n)
	}
}

// TestRouterRoutePathIsThePattern: the core sends the registered pattern as
// route_path and the real parameters in path_params. That must be an exact
// match that keeps the supplied parameters, not a segment match of the
// pattern's own ":name" segments (which would overwrite them with ":name").
func TestRouterRoutePathIsThePattern(t *testing.T) {
	r := NewRouter()
	var seen string
	r.Handle("DELETE", "/blocks/:user_id", func(_ context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		seen = Param(req, "user_id")
		return DataResponse(nil), nil
	})
	req := &pluginv1.HTTPRequest{
		Method: "DELETE", Path: "/blocks/3", RoutePath: "/blocks/:user_id",
		PathParams: map[string]string{"user_id": "3"},
	}
	resp, err := r.HandleHTTP(context.Background(), req)
	if err != nil || resp.GetStatus() != 200 {
		t.Fatalf("resp = %v %v", resp, err)
	}
	if seen != "3" {
		t.Fatalf("user_id = %q, want 3", seen)
	}
}

// TestRouterZeroValue: a Router{} works without NewRouter (the SDK used to keep
// two route tables, so only the second one was nil-checked and the first
// assignment panicked).
func TestRouterZeroValue(t *testing.T) {
	var r Router
	if resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", Path: "/x"}); err != nil || resp.GetStatus() != 404 {
		t.Fatalf("empty zero-value router: %v %v", resp, err)
	}
	r.Handle("GET", "/x", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		return DataResponse("x"), nil
	})
	resp, err := r.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", Path: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.GetBody()); got != `{"data":"x"}` {
		t.Fatalf("body = %s", got)
	}
}

// The ticket of a route call is read from its own body: a call without one,
// or with a body that is not JSON, is refused.
func TestRouteProxyTicket(t *testing.T) {
	for _, tc := range []struct{ body, ticket, field string }{
		{`{"proxy_ticket":"t-1","session_id":"s"}`, "t-1", ""},
		{`{"session_id":"s"}`, "", "proxy_ticket"},
		{`{"proxy_ticket":"  "}`, "", "proxy_ticket"},
		{``, "", "proxy_ticket"},
		{`{`, "", "body"},
	} {
		ticket, refused := RouteProxyTicket(&pluginv1.HTTPRequest{Body: []byte(tc.body)})
		switch {
		case tc.field == "" && (refused != nil || ticket != tc.ticket):
			t.Errorf("%q: ticket %q, refused %v", tc.body, ticket, refused)
		case tc.field != "" && (refused == nil || refused.GetStatus() != 400 || !strings.Contains(string(refused.GetBody()), `"field":"`+tc.field+`"`)):
			t.Errorf("%q: want a 400 on %s, got %v", tc.body, tc.field, refused)
		}
	}
}
