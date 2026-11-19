package core

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Environment variables (CONTRACTS §74.3) are the general runtime parameters
// of forwarding that the core declares and an administrator overrides per
// layer: account, then group, then the global setting, then the process
// environment variable. 0 (or an absent key) follows the next layer. Plugins
// read the resolved values and cannot declare their own. The process layer is
// read by config.Load and folded into the global layer by its owner
// (EnvironmentResolver.GlobalEnvironment).

// Environment variable keys, shared with plugins through the SDK.
const (
	EnvVarRelayTimeoutSeconds          = protocol.EnvVarRelayTimeoutSeconds
	EnvVarResponseHeaderTimeoutSeconds = protocol.EnvVarResponseHeaderTimeoutSeconds
	EnvVarStreamingIdleTimeoutSeconds  = protocol.EnvVarStreamingIdleTimeoutSeconds
	EnvVarTaskTimeoutMinutes           = protocol.EnvVarTaskTimeoutMinutes
	EnvVarTaskPollMaxFailures          = protocol.EnvVarTaskPollMaxFailures
)

// EnvironmentLayer is a layer an environment variable can be set on.
type EnvironmentLayer string

const (
	EnvLayerGlobal  EnvironmentLayer = "global"
	EnvLayerGroup   EnvironmentLayer = "group"
	EnvLayerAccount EnvironmentLayer = "account"
)

// EnvironmentVariable declares one variable.
type EnvironmentVariable struct {
	Key  string        `json:"key"`
	Unit string        `json:"unit"` // seconds | minutes | count
	Min  int64         `json:"min"`
	Max  int64         `json:"max"`
	Name LocalizedText `json:"name"`
	Help LocalizedText `json:"help"`
	// Layers it can be set on.
	Layers []EnvironmentLayer `json:"layers"`
	// ProcessEnv is the process environment variable the global layer falls
	// back to; empty without a global layer.
	ProcessEnv string `json:"process_env"`
}

var (
	allLayers = []EnvironmentLayer{EnvLayerGlobal, EnvLayerGroup, EnvLayerAccount}
	// taskLayers: the global layer of the async task variables is the
	// reconcile settings (CONTRACTS §8), not a second setting of its own.
	taskLayers = []EnvironmentLayer{EnvLayerGroup, EnvLayerAccount}
)

// EnvironmentVariables is the declared set, in display order.
var EnvironmentVariables = []EnvironmentVariable{
	{Key: EnvVarRelayTimeoutSeconds, Unit: "seconds", Min: 1, Max: 86400, Layers: allLayers, ProcessEnv: "SUB2API_RELAY_TIMEOUT_SECONDS",
		Name: LocalizedText{"en": "Upstream timeout", "zh": "上游整体超时"},
		Help: LocalizedText{"en": "Longest time one upstream attempt may take, including reading a streamed response. Unset: no limit.",
			"zh": "一次上游尝试的最长耗时，包含读取流式响应。未设置：不限。"}},
	{Key: EnvVarResponseHeaderTimeoutSeconds, Unit: "seconds", Min: 1, Max: 86400, Layers: allLayers, ProcessEnv: "SUB2API_RESPONSE_HEADER_TIMEOUT_SECONDS",
		Name: LocalizedText{"en": "Response header timeout", "zh": "响应头超时"},
		Help: LocalizedText{"en": "Longest wait for the upstream response headers. Unset: 180 s for streams, 600 s otherwise.",
			"zh": "等待上游响应头的最长时间。未设置：流式 180 秒，非流式 600 秒。"}},
	{Key: EnvVarStreamingIdleTimeoutSeconds, Unit: "seconds", Min: 1, Max: 86400, Layers: allLayers, ProcessEnv: "SUB2API_STREAMING_IDLE_TIMEOUT_SECONDS",
		Name: LocalizedText{"en": "Stream idle timeout", "zh": "流式空闲超时"},
		Help: LocalizedText{"en": "Longest gap between two reads of a streamed response. Unset: 180 s.",
			"zh": "流式响应两次读到数据之间的最长间隔。未设置：180 秒。"}},
	{Key: EnvVarTaskTimeoutMinutes, Unit: "minutes", Min: 1, Max: 43200, Layers: taskLayers,
		Name: LocalizedText{"en": "Async task timeout", "zh": "异步任务超时"},
		Help: LocalizedText{"en": "How long the core keeps observing an async task (image, video). Unset: the reconcile settings.",
			"zh": "核心观测一个异步任务（画图、视频）的最长时间。未设置：沿用对账设置。"}},
	{Key: EnvVarTaskPollMaxFailures, Unit: "count", Min: 1, Max: 1000, Layers: taskLayers,
		Name: LocalizedText{"en": "Task query failure limit", "zh": "任务查询失败上限"},
		Help: LocalizedText{"en": "Consecutive failed queries after which an async task is given up. Unset: the reconcile settings.",
			"zh": "异步任务连续查询失败多少次后放弃。未设置：沿用对账设置。"}},
}

// EnvironmentVariableByKey finds a declared variable.
func EnvironmentVariableByKey(key string) (EnvironmentVariable, bool) {
	i := slices.IndexFunc(EnvironmentVariables, func(v EnvironmentVariable) bool { return v.Key == key })
	if i < 0 {
		return EnvironmentVariable{}, false
	}
	return EnvironmentVariables[i], true
}

// AllowedOn reports whether the variable can be set on the layer.
func (v EnvironmentVariable) AllowedOn(layer EnvironmentLayer) bool {
	return slices.Contains(v.Layers, layer)
}

// EnvironmentValues are the values set on one layer; only set keys are kept.
type EnvironmentValues map[string]int64

// ValidateEnvironmentValues checks the values of one layer and drops the
// zeros (0 follows the next layer). field prefixes the field errors.
func ValidateEnvironmentValues(ctx context.Context, layer EnvironmentLayer, field string, in EnvironmentValues) (EnvironmentValues, []FieldError) {
	out := EnvironmentValues{}
	var fe []FieldError
	for _, key := range slices.Sorted(maps.Keys(in)) {
		value, f := in[key], field+"."+key
		v, ok := EnvironmentVariableByKey(key)
		switch {
		case !ok:
			fe = append(fe, FieldError{Field: f, Code: "invalid", Message: Text(ctx, "unknown environment variable", "未知的环境变量")})
		case !v.AllowedOn(layer):
			fe = append(fe, FieldError{Field: f, Code: "invalid", Message: Text(ctx, "cannot be set on this layer", "不能在这一层设置")})
		case value == 0:
		case value < v.Min || value > v.Max:
			fe = append(fe, FieldError{Field: f, Code: "invalid",
				Message: Text(ctx, "0 (follow) or "+rangeText(v), "0（跟随）或 "+rangeText(v))})
		default:
			out[key] = value
		}
	}
	return out, fe
}

func rangeText(v EnvironmentVariable) string {
	return strconv.FormatInt(v.Min, 10) + "-" + strconv.FormatInt(v.Max, 10)
}

// Environment is the resolved set: every key some layer sets, with the value
// of the nearest layer. A missing key means the component's own default.
type Environment map[string]int64

// ResolveEnvironment merges the layers, nearest first. A value a layer may
// not hold (written before a variable lost that layer) or out of range is
// skipped rather than trusted.
func ResolveEnvironment(global, group, account EnvironmentValues) Environment {
	layers := [...]struct {
		layer  EnvironmentLayer
		values EnvironmentValues
	}{{EnvLayerAccount, account}, {EnvLayerGroup, group}, {EnvLayerGlobal, global}}
	out := Environment{}
	for _, v := range EnvironmentVariables {
		for _, l := range layers {
			if x := l.values[v.Key]; x >= v.Min && x <= v.Max && v.AllowedOn(l.layer) {
				out[v.Key] = x
				break
			}
		}
	}
	return out
}

// Duration reads a seconds or minutes variable; 0 when unset.
func (e Environment) Duration(key string) time.Duration {
	v, ok := EnvironmentVariableByKey(key)
	if !ok || e[key] <= 0 {
		return 0
	}
	if v.Unit == "minutes" {
		return time.Duration(e[key]) * time.Minute
	}
	return time.Duration(e[key]) * time.Second
}

// EnvironmentResolver (owner: environment) resolves the environment of a
// group and an account outside a gateway request, e.g. for
// HostService.GetEnvironment. accountID 0 resolves the group alone.
type EnvironmentResolver interface {
	ResolveEnvironment(ctx context.Context, groupID, accountID int64) (Environment, error)
	// GlobalEnvironment is the global layer over the process layer, cached.
	GlobalEnvironment(ctx context.Context) EnvironmentValues
}
