package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func noop(context.Context, core.TaskRun) (core.TaskResult, error) { return core.TaskResult{}, nil }

func spec(name string, scope core.TaskScope) core.TaskSpec {
	return core.TaskSpec{Name: name, Title: core.LocalizedText{"en": "Test", "zh": "测试"}, Scope: scope,
		Every: time.Minute, Timeout: 10 * time.Second, Handler: noop}
}

func TestRegistryValidates(t *testing.T) {
	cases := map[string]func(*core.TaskSpec){
		"bad name":                 func(s *core.TaskSpec) { s.Name = "core.Account.x" },
		"two segments":             func(s *core.TaskSpec) { s.Name = "core.account" },
		"no title zh":              func(s *core.TaskSpec) { s.Title = core.LocalizedText{"en": "x"} },
		"no handler":               func(s *core.TaskSpec) { s.Handler = nil },
		"no timeout":               func(s *core.TaskSpec) { s.Timeout = 0 },
		"timeout over an hour":     func(s *core.TaskSpec) { s.Timeout = 61 * time.Minute },
		"every and cron":           func(s *core.TaskSpec) { s.Cron = "0 3 * * *" },
		"bad cron":                 func(s *core.TaskSpec) { s.Every, s.Cron = 0, "61 * * * *" },
		"cron that is every":       func(s *core.TaskSpec) { s.Every, s.Cron = 0, "@every 1m" },
		"sub-second every":         func(s *core.TaskSpec) { s.Every = 1500 * time.Millisecond },
		"cluster every below 5s":   func(s *core.TaskSpec) { s.Every = 4 * time.Second },
		"cluster RequireWork":      func(s *core.TaskSpec) { s.RequireWork = true },
		"too many attempts":        func(s *core.TaskSpec) { s.MaxAttempts = 26 },
		"run on start no schedule": func(s *core.TaskSpec) { s.Every, s.RunOnStart = 0, true },
		"bad scope":                func(s *core.TaskSpec) { s.Scope = "everywhere" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := spec("core.test.task", core.TaskCluster)
			mutate(&s)
			_, err := NewRegistry().Add(s, SourceCore, "")
			require.ErrorIs(t, err, core.ErrTaskInvalid)
		})
	}
	node := map[string]func(*core.TaskSpec){
		"no schedule":        func(s *core.TaskSpec) { s.Every = 0 },
		"timeout over 10m":   func(s *core.TaskSpec) { s.Timeout = 11 * time.Minute },
		"attempts on a node": func(s *core.TaskSpec) { s.MaxAttempts = 2 },
	}
	for name, mutate := range node {
		t.Run("node "+name, func(t *testing.T) {
			s := spec("core.test.task", core.TaskNode)
			mutate(&s)
			_, err := NewRegistry().Add(s, SourceCore, "")
			require.ErrorIs(t, err, core.ErrTaskInvalid)
		})
	}
}

func TestRegistryNamesAndSources(t *testing.T) {
	r := NewRegistry()
	_, err := r.Add(spec("core.test.task", core.TaskCluster), SourceCore, "")
	require.NoError(t, err)
	_, err = r.Add(spec("core.test.task", core.TaskNode), SourceCore, "")
	require.ErrorIs(t, err, core.ErrTaskInvalid, "duplicate name")

	_, err = r.Add(spec("plugin.dynweight.evaluate", core.TaskCluster), SourceCore, "")
	require.ErrorIs(t, err, core.ErrTaskInvalid, "core source with a plugin name")
	_, err = r.Add(spec("plugin.dynweight.evaluate", core.TaskCluster), SourcePluginManifest, "guard")
	require.ErrorIs(t, err, core.ErrTaskInvalid, "another plugin's name space")
	s := spec("plugin.dynweight.evaluate-v2.fast", core.TaskCluster)
	s.Title = nil // plugin tasks fall back to their id
	_, err = r.Add(s, SourcePluginRuntime, "dynweight")
	require.NoError(t, err)
	_, err = r.Add(spec("plugin._erio__codex.sync", core.TaskCluster), SourcePluginManifest, "_erio__codex")
	require.NoError(t, err, "a third-party plugin id")

	node := spec("core.test.node", core.TaskNode)
	node.Every = time.Second
	_, err = r.Add(node, SourceCore, "")
	require.NoError(t, err)

	names := func(ts []*Task) (out []string) {
		for _, t := range ts {
			out = append(out, t.Name)
		}
		return out
	}
	require.Equal(t, []string{"core.test.task", "plugin._erio__codex.sync", "plugin.dynweight.evaluate-v2.fast"}, names(r.List(core.TaskCluster)))
	require.Equal(t, []string{"core.test.node"}, names(r.List(core.TaskNode)))
	require.Len(t, r.List(""), 4)
	require.True(t, r.Remove("core.test.node"))
	require.False(t, r.Remove("core.test.node"))
	_, ok := r.Get("core.test.node")
	require.False(t, ok)
}

func TestTaskScheduleAndAttempts(t *testing.T) {
	r := NewRegistry()
	add := func(name string, mutate func(*core.TaskSpec)) *Task {
		s := spec(name, core.TaskCluster)
		mutate(&s)
		task, err := r.Add(s, SourceCore, "")
		require.NoError(t, err)
		return task
	}
	minute := add("core.test.minute", func(*core.TaskSpec) {})
	require.Equal(t, "@every 1m0s", minute.ScheduleText)
	require.Equal(t, time.Minute, minute.Period)
	require.Equal(t, 1, minute.Attempts(), "a slot every minute is its own retry")

	hourly := add("core.test.hourly", func(s *core.TaskSpec) { s.Every, s.Cron = 0, "17 * * * *" })
	require.Equal(t, "17 * * * *", hourly.ScheduleText)
	require.Equal(t, time.Hour, hourly.Period)
	require.Equal(t, 5, hourly.Attempts())

	onDemand := add("core.test.on_demand", func(s *core.TaskSpec) { s.Every = 0 })
	require.Nil(t, onDemand.Schedule)
	require.Equal(t, "", onDemand.ScheduleText)
	require.Equal(t, 5, onDemand.Attempts())

	explicit := add("core.test.explicit", func(s *core.TaskSpec) { s.MaxAttempts = 3 })
	require.Equal(t, 3, explicit.Attempts())
}

func TestScheduleAlignmentAndMinPeriod(t *testing.T) {
	at := time.Date(2026, 11, 3, 12, 0, 7, 0, time.UTC)
	require.Equal(t, time.Date(2026, 11, 3, 12, 0, 10, 0, time.UTC), Every(5*time.Second).Next(at))
	s, err := ParseSchedule("@every 1m")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 3, 12, 1, 0, 0, time.UTC), s.Next(at), "@every is aligned, not relative")

	daily, err := ParseSchedule("0 3 * * *")
	require.NoError(t, err)
	shanghai := at.In(time.FixedZone("CST", 8*3600))
	require.Equal(t, time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC), daily.Next(shanghai).UTC(), "cron runs in UTC")

	for spec, want := range map[string]time.Duration{
		"0 3 * * *":     24 * time.Hour,
		"*/5 * * * *":   5 * time.Minute,
		"0 */7 * * *":   3 * time.Hour, // 21:00 -> 00:00
		"0 3,4 * * *":   time.Hour,
		"30 2 * * 1":    7 * 24 * time.Hour,
		"@every 10s":    10 * time.Second,
		"0 0 1 * *":     28 * 24 * time.Hour,
		"15,45 * * * *": 30 * time.Minute,
	} {
		s, err := ParseSchedule(spec)
		require.NoError(t, err, spec)
		require.Equal(t, want, MinPeriod(s), spec)
	}
	_, err = ParseSchedule("not a schedule")
	require.Error(t, err)
	require.False(t, errors.Is(err, core.ErrTaskInvalid))
}
