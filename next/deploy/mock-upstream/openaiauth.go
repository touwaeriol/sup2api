package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The OpenAI authorization server (auth.openai.com), only the requests the
// codex plugin's two logins send (plugins/codex/internal/codex/oauth.go):
//
//	POST /api/accounts/deviceauth/usercode {client_id}
//	     -> {device_auth_id, user_code, interval: "1"}
//	POST /api/accounts/deviceauth/token {device_auth_id, user_code}
//	     -> the first poll of a code: 403 (not approved yet); later polls:
//	        {authorization_code: "code-<device_auth_id>", code_verifier}
//	POST /oauth/token (form) grant_type=authorization_code, code
//	     -> {access_token: "at-<code>", refresh_token, id_token, expires_in}
//
// Nothing is verified beyond the fields being there: the tests check what
// the mock recorded (who sent it, through which proxy). The tokens name the
// code they came from, so a test can tell which login made an account; the
// id_token carries chatgpt_account_id "acct-e2e" (the codex accounts of
// codexCreds in next/e2e).

func (s *server) openAIAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/accounts/deviceauth/usercode", s.deviceUserCode)
	mux.HandleFunc("POST /api/accounts/deviceauth/token", s.deviceToken)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
}

// authAnswer records the request with status and writes v.
func (s *server) authAnswer(w http.ResponseWriter, id int64, status int, v any) {
	s.setStatus(id, status)
	writeJSON(w, status, v)
}

func (s *server) deviceUserCode(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	id := s.record(r, body, "", false, "")
	var in struct {
		ClientID string `json:"client_id"`
	}
	if json.Unmarshal(body, &in) != nil || in.ClientID == "" {
		s.authAnswer(w, id, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	s.authAnswer(w, id, http.StatusOK, map[string]any{
		"device_auth_id": fmt.Sprintf("dev-%d", id), "user_code": fmt.Sprintf("MOCK-%d", id), "interval": "1",
	})
}

func (s *server) deviceToken(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	id := s.record(r, body, "", false, "")
	var in struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
	}
	if json.Unmarshal(body, &in) != nil || in.DeviceAuthID == "" || in.UserCode == "" {
		s.authAnswer(w, id, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	s.mu.Lock()
	approved := s.devicePolled[in.DeviceAuthID]
	s.devicePolled[in.DeviceAuthID] = true
	s.mu.Unlock()
	if !approved {
		s.authAnswer(w, id, http.StatusForbidden, map[string]any{"error": "authorization_pending"})
		return
	}
	s.authAnswer(w, id, http.StatusOK, map[string]any{
		"authorization_code": "code-" + in.DeviceAuthID, "code_verifier": "verifier-" + in.DeviceAuthID,
	})
}

func (s *server) oauthToken(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	id := s.record(r, body, "", false, "")
	form, err := url.ParseQuery(string(body))
	code := form.Get("code")
	if err != nil || form.Get("grant_type") != "authorization_code" || code == "" {
		s.authAnswer(w, id, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
		return
	}
	s.authAnswer(w, id, http.StatusOK, map[string]any{
		"access_token": "at-" + code, "refresh_token": "rt-" + code, "id_token": mockIDToken(), "expires_in": 86400,
	})
}

// mockIDToken is an unsigned JWT with the claims the codex plugin reads.
func mockIDToken() string {
	seg := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return seg(map[string]any{"alg": "none"}) + "." + seg(map[string]any{
		"email":                       "codex@e2e.test",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-e2e", "chatgpt_plan_type": "plus"},
	}) + ".mock"
}
