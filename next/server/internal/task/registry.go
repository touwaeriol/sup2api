// Package task is the scheduled task framework (TASK-FRAMEWORK-DESIGN): one
// registry behind core.Tasks, gocron for tasks every node runs and River for
// tasks the cluster runs once per slot.
package task

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Source says who registered a task.
type Source string

const (
	SourceCore           Source = "core"
	SourcePluginManifest Source = "plugin_manifest"
	SourcePluginRuntime  Source = "plugin_runtime"
)

// Limits of a spec (TASK-FRAMEWORK-DESIGN §4, §7.6). River would wait out a
// longer cluster timeout before rescuing a run (it checks Worker.Timeout),
// but a core release stops a run within seconds: longer work continues in
// further runs (core.TaskContinue). Plugin manifests are held to the same
// limits (manifest.MaxClusterJobTimeoutSec, checked by internal/job tests).
const (
	MaxClusterTimeout = time.Hour
	MaxNodeTimeout    = 10 * time.Minute
	MinClusterEvery   = 5 * time.Second
	MinNodeEvery      = time.Second
	MaxAttemptsLimit  = 25
	// retryEvery is the period from which periodic cluster tasks retry a
	// failed run by default (MaxAttempts 5); more frequent ones do not.
	retryEvery = 10 * time.Minute
)

var (
	coreNameRe = regexp.MustCompile(`^core\.[a-z0-9_]+\.[a-z0-9_]+$`)
	// pluginNameRe: plugin.<plugin id>.<job id>; an id (manifest.PluginID)
	// starts with a letter, or "_" for a third-party plugin's.
	pluginNameRe = regexp.MustCompile(`^plugin\.([a-z_][a-z0-9_]{1,29})\.[a-z0-9][a-z0-9_.-]{0,63}$`)
)

const maxNameLen = 120

// Task is one registered task with its parsed schedule.
type Task struct {
	core.TaskSpec
	Source    Source
	PluginKey string
	// Schedule is nil for a cluster task that only runs enqueued work.
	Schedule     Schedule
	ScheduleText string
	// Period is MinPeriod(Schedule), 0 without a schedule.
	Period time.Duration
}

// queue is the River queue of the task's cluster runs.
func (t *Task) queue() string {
	if t.Source == SourceCore {
		return queueCore
	}
	return queuePlugin
}

// Attempts is the number of attempts of one cluster run.
func (t *Task) Attempts() int {
	if t.MaxAttempts > 0 {
		return t.MaxAttempts
	}
	if t.Schedule != nil && t.Period < retryEvery {
		return 1
	}
	return 5
}

// Registry holds the tasks of this node by name.
type Registry struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

func NewRegistry() *Registry { return &Registry{tasks: map[string]*Task{}} }

// Add validates spec and registers it.
func (r *Registry) Add(spec core.TaskSpec, source Source, pluginKey string) (*Task, error) {
	t, err := newTask(spec, source, pluginKey)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.tasks[t.Name]; dup {
		return nil, fmt.Errorf("%w: %s is already registered", core.ErrTaskInvalid, t.Name)
	}
	r.tasks[t.Name] = t
	return t, nil
}

// Remove unregisters name and reports whether it was registered.
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tasks[name]
	delete(r.tasks, name)
	return ok
}

// Get returns the task called name.
func (r *Registry) Get(name string) (*Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[name]
	return t, ok
}

// List returns the tasks of scope (all when empty), sorted by name.
func (r *Registry) List(scope core.TaskScope) []*Task {
	r.mu.RLock()
	out := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		if scope == "" || t.Scope == scope {
			out = append(out, t)
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func newTask(spec core.TaskSpec, source Source, pluginKey string) (*Task, error) {
	invalid := func(format string, args ...any) (*Task, error) {
		return nil, fmt.Errorf("%w: %s: %s", core.ErrTaskInvalid, spec.Name, fmt.Sprintf(format, args...))
	}
	if len(spec.Name) > maxNameLen {
		return invalid("name is longer than %d characters", maxNameLen)
	}
	switch source {
	case SourceCore:
		if pluginKey != "" || !coreNameRe.MatchString(spec.Name) {
			return invalid("a core task is named core.<package>.<task> (%s)", coreNameRe)
		}
	case SourcePluginManifest, SourcePluginRuntime:
		m := pluginNameRe.FindStringSubmatch(spec.Name)
		if m == nil || m[1] != pluginKey {
			return invalid("a task of plugin %q is named plugin.%s.<job id>", pluginKey, pluginKey)
		}
	default:
		return invalid("unknown source %q", source)
	}
	if source == SourceCore && (spec.Title["en"] == "" || spec.Title["zh"] == "") {
		return invalid("Title needs en and zh")
	}
	if spec.Handler == nil {
		return invalid("Handler is required")
	}
	sched, text, err := specSchedule(spec.Every, spec.Cron)
	if err != nil {
		return invalid("%v", err)
	}
	t := &Task{TaskSpec: spec, Source: source, PluginKey: pluginKey, Schedule: sched, ScheduleText: text}
	if sched != nil {
		t.Period = MinPeriod(sched)
	}
	switch spec.Scope {
	case core.TaskNode:
		if sched == nil {
			return invalid("a node task needs Every or Cron")
		}
		if spec.Every > 0 && spec.Every < MinNodeEvery {
			return invalid("Every must be at least %s", MinNodeEvery)
		}
		if spec.Timeout <= 0 || spec.Timeout > MaxNodeTimeout {
			return invalid("Timeout must be in (0, %s]", MaxNodeTimeout)
		}
		if spec.MaxAttempts != 0 {
			return invalid("MaxAttempts is for cluster tasks")
		}
	case core.TaskCluster:
		if spec.Every > 0 && spec.Every < MinClusterEvery {
			return invalid("Every must be at least %s", MinClusterEvery)
		}
		if spec.Timeout <= 0 || spec.Timeout > MaxClusterTimeout {
			return invalid("Timeout must be in (0, %s]", MaxClusterTimeout)
		}
		if spec.RequireWork {
			return invalid("RequireWork is for node tasks; cluster tasks always need admission")
		}
		if spec.MaxAttempts < 0 || spec.MaxAttempts > MaxAttemptsLimit {
			return invalid("MaxAttempts must be in [0, %d]", MaxAttemptsLimit)
		}
		if sched == nil && spec.RunOnStart {
			return invalid("RunOnStart needs a schedule")
		}
	default:
		return invalid("Scope must be node or cluster")
	}
	return t, nil
}

// Describe validates spec as a task of source and pluginKey without
// registering it and returns what registering it would record (schedule
// text, attempts).
func Describe(spec core.TaskSpec, source Source, pluginKey string) (*Task, error) {
	return newTask(spec, source, pluginKey)
}
