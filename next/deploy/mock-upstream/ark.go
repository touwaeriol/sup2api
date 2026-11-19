package main

import "net/http"

// arkAssetPath is the asset library OpenAPI of the e2e Ark accounts: a relay
// account's asset_endpoint (CONTRACTS §124, AC43). Every Action is
// recorded and answered with an empty result.
const arkAssetPath = "/__ark/asset"

func (s *server) arkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+arkAssetPath, func(w http.ResponseWriter, r *http.Request) {
		id := s.record(r, readBody(r), "", false, "")
		s.setStatus(id, http.StatusOK)
		writeJSON(w, http.StatusOK, map[string]any{
			"ResponseMetadata": map[string]any{"RequestId": "mock-ark", "Action": r.URL.Query().Get("Action")},
			"Result":           map[string]any{"Items": []any{}, "TotalCount": 0, "PageNumber": 1, "PageSize": 10},
		})
	})
}
