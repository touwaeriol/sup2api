package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestToolCallMessages(t *testing.T) {
	srv := httptest.NewServer(newServer().handler())
	defer srv.Close()
	tools := `"tools":[{"name":"lookup","input_schema":{"type":"object"}}]`
	ask := `{"model":"m",` + tools + `,"messages":[{"role":"user","content":"MOCK-TOOL-CALL"}]}`
	m := decode(t, post(t, srv, "/v1/messages", nil, ask))
	if m["stop_reason"] != "tool_use" || at(m, "content", "0", "name") != "lookup" || at(m, "content", "0", "input", "query") != "mock" {
		t.Fatalf("tool call: %v", m)
	}
	stream := strings.Replace(ask, `"model":"m",`, `"model":"m","stream":true,`, 1)
	_, data := sseData(t, post(t, srv, "/v1/messages", nil, stream))
	joined := strings.Join(data, "\n")
	if !strings.Contains(joined, `"input_json_delta"`) || !strings.Contains(joined, `"stop_reason":"tool_use"`) {
		t.Fatalf("streamed tool call: %s", joined)
	}
	answered := `{"model":"m",` + tools + `,"messages":[{"role":"user","content":"MOCK-TOOL-CALL"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"lookup","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"42"}]}]}`
	m = decode(t, post(t, srv, "/v1/messages", nil, answered))
	if m["stop_reason"] != "end_turn" || !strings.HasPrefix(at(m, "content", "0", "text").(string), "TOOL-RESULT-SEEN") {
		t.Fatalf("after the tool result: %v", m)
	}
}

func TestToolCallChat(t *testing.T) {
	srv := httptest.NewServer(newServer().handler())
	defer srv.Close()
	tools := `"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`
	ask := `{"model":"m",` + tools + `,"messages":[{"role":"user","content":"MOCK-TOOL-CALL"}]}`
	m := decode(t, post(t, srv, "/v1/chat/completions", nil, ask))
	if at(m, "choices", "0", "finish_reason") != "tool_calls" || at(m, "choices", "0", "message", "tool_calls", "0", "function", "arguments") != `{"query":"mock"}` {
		t.Fatalf("tool call: %v", m)
	}
	stream := strings.Replace(ask, `"model":"m",`, `"model":"m","stream":true,`, 1)
	_, data := sseData(t, post(t, srv, "/v1/chat/completions", nil, stream))
	joined := strings.Join(data, "\n")
	if !strings.Contains(joined, `"finish_reason":"tool_calls"`) || !strings.Contains(joined, `"arguments":"\"mock\"}"`) {
		t.Fatalf("streamed tool call: %s", joined)
	}
	answered := `{"model":"m",` + tools + `,"messages":[{"role":"user","content":"MOCK-TOOL-CALL"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"c","content":"42"}]}`
	m = decode(t, post(t, srv, "/v1/chat/completions", nil, answered))
	if !strings.HasPrefix(at(m, "choices", "0", "message", "content").(string), "TOOL-RESULT-SEEN") {
		t.Fatalf("after the tool result: %v", m)
	}
}

func TestTLSAndProxyRecording(t *testing.T) {
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("MOCK_TLS_ADDR", addr)
	t.Setenv("MOCK_TLS_DIR", dir)
	s := newServer()
	s.tunnels = newTunnelListener()
	defer s.tunnels.Close()
	go func() { _ = serveTLS(s.handler(), s.tunnels) }()
	var pemCA []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(pemCA) == 0 && time.Now().Before(deadline) {
		pemCA, _ = os.ReadFile(filepath.Join(dir, "ca.pem"))
		time.Sleep(20 * time.Millisecond)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemCA) {
		t.Fatal("no CA written")
	}
	dialer := &net.Dialer{}
	cl := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}}
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = cl.Post("https://vendor.example.com/v1/messages", "application/json", strings.NewReader(`{"model":"m"}`))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// A forward-proxy request (absolute URI) on the plain listener.
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body := `{"model":"m"}`
	_, _ = conn.Write([]byte("POST http://upstream.example/v1/messages HTTP/1.1\r\nHost: upstream.example\r\nContent-Type: application/json\r\nContent-Length: 13\r\nConnection: close\r\n\r\n" + body))
	buf := make([]byte, 512)
	_, _ = conn.Read(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.log) != 2 || !s.log[0].TLS || s.log[0].Host != "vendor.example.com" || s.log[0].Proxied {
		t.Fatalf("TLS request: %+v", s.log)
	}
	if !s.log[1].Proxied || s.log[1].Host != "upstream.example" || s.log[1].TLS {
		t.Fatalf("proxied request: %+v", s.log[1])
	}
}

// at is get with array indices ("0") along the path.
func at(m any, path ...string) any {
	for _, p := range path {
		switch v := m.(type) {
		case map[string]any:
			m = v[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i >= len(v) {
				return nil
			}
			m = v[i]
		default:
			return nil
		}
	}
	return m
}
