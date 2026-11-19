package protocol

// Environment variable keys (CONTRACTS §74.3). The core declares them;
// plugins read the resolved values from RequestMeta.environment or
// HostService.GetEnvironment.
const (
	EnvVarRelayTimeoutSeconds          = "relay_timeout_seconds"
	EnvVarResponseHeaderTimeoutSeconds = "response_header_timeout_seconds"
	EnvVarStreamingIdleTimeoutSeconds  = "streaming_idle_timeout_seconds"
	EnvVarTaskTimeoutMinutes           = "task_timeout_minutes"
	EnvVarTaskPollMaxFailures          = "task_poll_max_failures"
)

// EnvVarRange is the accepted range of a set variable; 0 follows the next
// layer.
type EnvVarRange struct{ Min, Max int64 }

// AccountEnvVars are the variables the account layer may set, with their
// ranges: what manifest/check accepts as a commonControls.environment
// default. The core's declarations (server core.EnvironmentVariables) are
// checked against it in their tests.
var AccountEnvVars = map[string]EnvVarRange{
	EnvVarRelayTimeoutSeconds:          {1, 86400},
	EnvVarResponseHeaderTimeoutSeconds: {1, 86400},
	EnvVarStreamingIdleTimeoutSeconds:  {1, 86400},
	EnvVarTaskTimeoutMinutes:           {1, 43200},
	EnvVarTaskPollMaxFailures:          {1, 1000},
}
