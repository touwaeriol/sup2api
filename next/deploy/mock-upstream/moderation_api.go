package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Moderation API simulation for moderation 0.2 auditors (CONTRACTS §72.16).
// Every category scores 0.01; a "MOD-HIT-<category>" token anywhere in the
// request scores that category 0.99 (category codes may contain / - _), and
// "MOD-500" answers 500.
//
//	POST /v1/moderations  {model, input}            -> results[0].category_scores
//	POST /v1/systemone    {model, state, questions} -> answers[q] = {type:"noul", noul};
//	                      usage.input_tokens is the behaviour's input (rules apply), output 0

var modHitToken = regexp.MustCompile(`MOD-HIT-([a-z0-9/_-]+)`)

var openAIModerationCategories = []string{
	"harassment", "harassment/threatening", "hate", "hate/threatening",
	"illicit", "illicit/violent", "self-harm", "self-harm/intent",
	"self-harm/instructions", "sexual", "sexual/minors", "violence", "violence/graphic",
}

func modScore(body []byte) func(string) float64 {
	hits := map[string]bool{}
	for _, m := range modHitToken.FindAllSubmatch(body, -1) {
		hits[string(m[1])] = true
	}
	return func(cat string) float64 {
		if hits[cat] {
			return 0.99
		}
		return 0.01
	}
}

func (s *server) moderations(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	id := s.record(r, body, req.Model, false, "")
	if strings.Contains(string(body), "MOD-500") {
		s.setStatus(id, 500)
		writeJSON(w, 500, map[string]any{"error": map[string]any{"message": "mock: moderation failure"}})
		return
	}
	score := modScore(body)
	scores := map[string]float64{}
	for _, c := range openAIModerationCategories {
		scores[c] = score(c)
	}
	s.setStatus(id, 200)
	writeJSON(w, 200, map[string]any{"id": "modr-mock", "model": req.Model, "results": []any{map[string]any{"category_scores": scores}}})
}

func (s *server) systemOne(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	var req struct {
		Model     string                     `json:"model"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	_ = json.Unmarshal(body, &req)
	id, b, ok := s.begin(w, r, body, req.Model, false, nil, formatOpenAI)
	if !ok {
		return
	}
	if strings.Contains(string(body), "MOD-500") {
		s.setStatus(id, 500)
		writeJSON(w, 500, map[string]any{"error": "mock: systemone failure"})
		return
	}
	score := modScore(body)
	answers := map[string]any{}
	for q := range req.Questions {
		answers[q] = map[string]any{"type": "noul", "noul": score(q)}
	}
	s.setStatus(id, 200)
	// TypeSafe bills the input only: output_tokens is always 0.
	writeJSON(w, 200, map[string]any{"model": req.Model, "answers": answers, "usage": map[string]int64{"input_tokens": b.usage.InputTokens, "output_tokens": 0}})
}
