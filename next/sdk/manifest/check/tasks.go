package check

import (
	"fmt"
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

func (v *validator) taskEndpoint(f string, e manifest.Endpoint) {
	t := e.Task
	if t == nil {
		return
	}
	if !v.standalone {
		v.needCap(f+".task", manifest.CapPlatformTasks)
	}
	if t.Action != manifest.TaskActionSubmit && t.Action != manifest.TaskActionQuery {
		v.add(f+".task.action", "invalid", "task action must be submit or query")
	}
	if !idRe.MatchString(t.Kind) {
		v.add(f+".task.kind", "invalid_format", "task kind must match %s", idRe.String())
	}
	if e.Request.Stream || e.Request.StreamPath != "" || e.Response.Stream != "" || e.Response.NonStream != "json" {
		v.add(f+".task", "unsupported", "task endpoints must use bounded, non-streaming JSON")
	}
	if len(t.IDPaths) == 0 || len(t.IDPaths) > protocol.MaxTaskIDPaths {
		v.add(f+".task.idPaths", "out_of_range", "declare 1 to %d task ID paths", protocol.MaxTaskIDPaths)
	}
	seen := map[string]bool{}
	for i, p := range t.IDPaths {
		pf := fmt.Sprintf("%s.task.idPaths[%d]", f, i)
		if !protocol.ValidTaskIDPath(p) {
			v.add(pf, "invalid_path", "task IDs require simple dot-separated JSON object paths")
		} else if seen[p] {
			v.add(pf, "duplicate", "duplicate task ID path %q", p)
		}
		seen[p] = true
	}
	if e.TaskQuery() {
		if e.Method != "GET" || e.Billing != "free" {
			v.add(f+".task", "conflict", "task queries must use GET and billing free")
		}
		if t.IDParam == "" || !slices.Contains(PathParams(e.Path), t.IDParam) {
			v.add(f+".task.idParam", "unknown_param", "task idParam must name a path parameter")
		}
		if e.Request.ModelPath != "" || e.Request.ModelParam != "" || e.Request.ModelSource != "" {
			v.add(f+".request", "conflict", "task query models come from the host's task record")
		}
	} else if e.TaskSubmit() {
		if e.Method != "POST" || t.IDParam != "" {
			v.add(f+".task", "conflict", "task submissions must use POST without idParam")
		}
		if !v.standalone {
			v.needCap(f+".task", manifest.CapPlatformAdapter)
		}
	}
}

// A kind is plugin-scoped, so it cannot accidentally resolve a different
// platform's query serializer or response ID paths.
func (v *validator) taskPairs(platforms []manifest.Platform) {
	type pair struct{ submit, query int }
	kinds := map[string]*pair{}
	for _, p := range platforms {
		for _, e := range p.Endpoints {
			if e.Task == nil {
				continue
			}
			t := e.Task
			if kinds[t.Kind] == nil {
				kinds[t.Kind] = &pair{}
			}
			if e.TaskSubmit() {
				kinds[t.Kind].submit++
			} else if e.TaskQuery() {
				kinds[t.Kind].query++
			}
		}
	}
	for _, kind := range sortedKeys(kinds) {
		p := kinds[kind]
		if p.submit != 1 || p.query != 1 {
			v.add("task."+kind, "unpaired", "task kind %q needs exactly one submit and one query endpoint", kind)
		}
	}
}
