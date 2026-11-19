package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// The video mock never advances by wall clock. Tests explicitly publish a
// terminal state; Block holds an observation until changed or its client dies.
// Controls are isolated by upstream account key, including future submissions.
type videoControl struct {
	APIKey       string `json:"api_key"`
	State        string `json:"state"`
	OutputTokens int64  `json:"output_tokens"`
	DelayMS      int    `json:"delay_ms"`
	Block        bool   `json:"block"`
}

type videoQuery struct {
	TaskID   string `json:"task_id"`
	APIKey   string `json:"api_key"`
	Source   string `json:"source"`
	Active   bool   `json:"active"`
	Canceled bool   `json:"canceled"`
	Status   int    `json:"status"`
}

type videoTask struct {
	ID        string       `json:"id"`
	APIKey    string       `json:"api_key"`
	Model     string       `json:"model"`
	Active    int          `json:"active"`
	MaxActive int          `json:"max_active"`
	Queries   []videoQuery `json:"queries"`
}

type videoStore struct {
	mu       sync.Mutex
	seq      int
	tasks    map[string]*videoTask
	controls map[string]videoControl
	changed  chan struct{}
}

func newVideoStore() *videoStore {
	return &videoStore{tasks: map[string]*videoTask{}, controls: map[string]videoControl{}, changed: make(chan struct{})}
}

func (s *server) controlVideo(w http.ResponseWriter, r *http.Request) {
	var c videoControl
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&c) != nil || c.APIKey == "" || c.DelayMS < 0 || c.DelayMS > 60000 || c.OutputTokens < 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid video control"})
		return
	}
	if c.State == "" {
		c.State = "running"
	}
	if c.State != "running" && c.State != "succeeded" && c.State != "failed" {
		writeJSON(w, 400, map[string]string{"error": "state must be running, succeeded or failed"})
		return
	}
	v := s.video
	v.mu.Lock()
	v.controls[c.APIKey] = c
	close(v.changed)
	v.changed = make(chan struct{})
	v.mu.Unlock()
	writeJSON(w, 200, c)
}

func (s *server) submitVideo(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var in struct {
		Model string `json:"model"`
	}
	if err != nil || json.Unmarshal(body, &in) != nil || in.Model == "" || requestKey(r) == "" {
		writeJSON(w, 400, map[string]string{"error": "model and account key required"})
		return
	}
	recordID := s.record(r, body, in.Model, false, "")
	// The generic behaviour rules (delay, injected status) apply to the
	// submission too, so a test can hold it while the gateway waits.
	b := s.resolve(r, nil)
	if !sleep(r, b.delay) {
		s.setStatus(recordID, 499)
		return
	}
	if b.status >= 400 {
		s.setStatus(recordID, b.status)
		writeOpenAIMockError(w, b.status)
		return
	}
	v := s.video
	v.mu.Lock()
	v.seq++
	id := fmt.Sprintf("mock-video-%06d", v.seq)
	v.tasks[id] = &videoTask{ID: id, APIKey: requestKey(r), Model: in.Model, Queries: []videoQuery{}}
	v.mu.Unlock()
	s.setStatus(recordID, 200)
	writeJSON(w, 200, map[string]any{"id": id, "model": in.Model, "status": "queued"})
}

func (s *server) queryVideo(w http.ResponseWriter, r *http.Request) {
	v, key, id := s.video, requestKey(r), r.PathValue("id")
	recordID := s.record(r, nil, "", false, "")
	source, _, _ := net.SplitHostPort(r.RemoteAddr)
	v.mu.Lock()
	task := v.tasks[id]
	if task == nil {
		v.mu.Unlock()
		s.setStatus(recordID, 404)
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	index := len(task.Queries)
	task.Queries = append(task.Queries, videoQuery{TaskID: id, APIKey: key, Source: source})
	if task.APIKey != key {
		task.Queries[index].Status = 404
		v.mu.Unlock()
		s.setStatus(recordID, 404)
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	task.Queries[index].Active = true
	task.Active++
	if task.Active > task.MaxActive {
		task.MaxActive = task.Active
	}
	v.mu.Unlock()
	defer func() {
		v.mu.Lock()
		task.Active--
		task.Queries[index].Active = false
		task.Queries[index].Canceled = r.Context().Err() != nil
		v.mu.Unlock()
	}()
	// Wait for a deliberate release, never hold the store mutex across network
	// I/O. A killed node cancels its outstanding request and drops Active.
	var c videoControl
	for {
		v.mu.Lock()
		c = v.controls[key]
		changed := v.changed
		v.mu.Unlock()
		if !c.Block {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
	if c.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(c.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
	}
	if c.State == "" {
		c.State = "running"
	}
	body := map[string]any{"id": id, "model": task.Model, "status": c.State}
	if c.State == "succeeded" {
		body["usage"] = map[string]int64{"completion_tokens": c.OutputTokens}
		body["content"] = map[string]string{"video_url": "https://mock.invalid/" + id + ".mp4", "resolution": "720p"}
	}
	if c.State == "failed" {
		body["error"] = map[string]string{"code": "mock_failed", "message": "deterministic video failure"}
	}
	v.mu.Lock()
	task.Queries[index].Status = 200
	v.mu.Unlock()
	s.setStatus(recordID, 200)
	writeJSON(w, 200, body)
}

func (s *server) videoStats(w http.ResponseWriter, r *http.Request) {
	v := s.video
	v.mu.Lock()
	defer v.mu.Unlock()
	tasks := []videoTask{}
	for _, task := range v.tasks {
		if key := r.URL.Query().Get("api_key"); key != "" && task.APIKey != key {
			continue
		}
		if id := r.URL.Query().Get("task_id"); id != "" && task.ID != id {
			continue
		}
		tasks = append(tasks, *task)
	}
	writeJSON(w, 200, map[string]any{"tasks": tasks})
}
