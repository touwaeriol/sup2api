package usagerules

import (
	"errors"
	"strconv"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
)

type attemptMeter struct {
	finalFacts     map[string]any
	started        bool
	complete       bool
	err            error
	model, reason  string
	usage, details gjson.Result
	boundaries     []gjson.Result
	pending        map[int64]gjson.Result
	seen           map[int64]bool
}

// WithAttemptAccounting is activated only from an admitted request contract.
// Raw response data cannot opt itself into a different billing strategy.
func (u *Acc) WithAttemptAccounting(active bool) *Acc {
	if active {
		u.attempts = &attemptMeter{}
		if u.rules.Attempts == nil || u.rules.Attempts.Adapter != manifest.AttemptAdapterAnthropicFallback {
			u.attempts.err = errors.New("attempt usage contract missing or unsupported")
		}
	}
	return u
}

func (u *Acc) Replacement() ([]core.AttemptUsage, error) {
	if u.attempts == nil {
		return nil, nil
	}
	m := u.attempts
	if m.err != nil {
		return nil, m.err
	}
	if !m.complete {
		return nil, errors.New("attempt usage has no complete terminal response")
	}
	items, err := m.anthropicFallback(u.rules.Attempts.Name, u.primaryModel)
	if err == nil && len(items) > 0 {
		for key, value := range m.finalFacts {
			if key == core.CacheWriteEvidenceKey {
				continue
			}
			if items[len(items)-1].Metrics == nil {
				items[len(items)-1].Metrics = map[string]any{}
			}
			items[len(items)-1].Metrics[key] = value
		}
	}
	return items, err
}
func (u *Acc) applyAttempts(event string, body []byte, stream bool) {
	m := u.attempts
	if m == nil || m.err != nil {
		return
	}
	if m.complete {
		m.err = errors.New("attempt usage received data after terminal")
		return
	}
	doc := gjson.ParseBytes(body)
	if !stream {
		u.applyAttemptsMessage(m, doc, body)
		return
	}
	u.applyAttemptEvent(m, event, doc, body)
}

// applyAttemptsMessage loads a non-streamed message response: the whole
// attempt chain is present at once.
func (u *Acc) applyAttemptsMessage(m *attemptMeter, doc gjson.Result, body []byte) {
	if !doc.IsObject() || doc.Get("type").Str != "message" {
		m.err = errors.New("attempt usage expected message response")
		return
	}
	m.model = doc.Get("model").Str
	if !doc.Get("content").IsArray() {
		m.err = errors.New("attempt response has invalid content")
		return
	}
	m.reason = doc.Get("stop_reason").Str
	m.usage = doc.Get("usage")
	m.details = doc.Get("stop_details")
	m.finalFacts = extractAttemptFacts(u.rules.Facts, body)
	for _, b := range doc.Get("content").Array() {
		if b.Get("type").Str == "fallback" {
			m.boundaries = append(m.boundaries, b)
		}
	}
	m.complete = true
}

// applyAttemptEvent advances the streamed state machine by one event.
func (u *Acc) applyAttemptEvent(m *attemptMeter, event string, doc gjson.Result, body []byte) {
	if !m.started && event != "message_start" && event != "ping" && event != "error" {
		m.err = errors.New("attempt event before message_start")
		return
	}
	switch event {
	case "message_start":
		if m.started {
			m.err = errors.New("duplicate attempt message_start")
			return
		}
		m.started = true
		m.model = doc.Get("message.model").Str
		m.usage = doc.Get("message.usage")
		m.details = doc.Get("message.stop_details")
	case "content_block_start":
		if doc.Get("content_block.type").Str == "fallback" {
			m.finalFacts = nil
			if len(m.boundaries)+len(m.pending) >= 3 {
				m.err = errors.New("too many fallback transitions")
				return
			}
			if m.pending == nil {
				m.pending = map[int64]gjson.Result{}
				m.seen = map[int64]bool{}
			}
			index, err := attemptBlockIndex(doc.Get("index"))
			if err != nil {
				m.err = err
				return
			}
			if m.seen[index] {
				m.err = errors.New("duplicate fallback block index")
				return
			}
			m.seen[index] = true
			m.pending[index] = doc.Get("content_block")
		}
	case "content_block_stop":
		index, err := attemptBlockIndex(doc.Get("index"))
		if err != nil {
			m.err = err
			return
		}
		if b, ok := m.pending[index]; ok {
			m.boundaries = append(m.boundaries, b)
			delete(m.pending, index)
		}
	case "message_delta":
		// Only actual facts in this terminal attempt's delta are eligible.
		// Do not inherit message_start facts from a model that later declined.
		m.finalFacts = extractAttemptFacts(u.rules.Facts, body)
		if value := doc.Get("delta.stop_reason"); value.Exists() && value.Type != gjson.Null {
			m.reason = value.Str
		}
		if d := doc.Get("delta.stop_details"); d.Exists() {
			m.details = d
		}
		if d := doc.Get("stop_details"); d.Exists() {
			m.details = d
		}
		m.usage = mergeUsageSnapshot(m.usage, doc.Get("usage"))
	case "message_stop":
		if len(m.pending) != 0 {
			m.err = errors.New("unclosed fallback block")
		}
		m.complete = true
	case "error":
		m.err = errors.New("attempt usage stream ended with upstream error")
	}
}

func attemptBlockIndex(value gjson.Result) (int64, error) {
	n, err := strconv.ParseInt(value.Raw, 10, 64)
	if value.Type != gjson.Number || err != nil || n < 0 || n > 1000000 {
		return 0, errors.New("invalid fallback block index")
	}
	return n, nil
}
