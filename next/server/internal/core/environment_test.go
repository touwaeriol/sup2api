package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

func TestResolveEnvironmentNearestLayerWins(t *testing.T) {
	env := ResolveEnvironment(
		// The async task variables have no global layer (the reconcile settings are).
		EnvironmentValues{EnvVarRelayTimeoutSeconds: 60, EnvVarResponseHeaderTimeoutSeconds: 8, EnvVarTaskTimeoutMinutes: 30, EnvVarTaskPollMaxFailures: 5},
		EnvironmentValues{EnvVarRelayTimeoutSeconds: 120, EnvVarResponseHeaderTimeoutSeconds: 16, EnvVarTaskTimeoutMinutes: 30},
		// A key that is not declared (a row written before a variable was removed) is ignored.
		EnvironmentValues{EnvVarRelayTimeoutSeconds: 600, "max_request_body_mb": 64, EnvVarStreamingIdleTimeoutSeconds: 999999},
	)
	require.EqualValues(t, 600, env[EnvVarRelayTimeoutSeconds])
	require.EqualValues(t, 16, env[EnvVarResponseHeaderTimeoutSeconds])
	require.NotContains(t, env, "max_request_body_mb")
	require.EqualValues(t, 30, env[EnvVarTaskTimeoutMinutes])
	_, set := env[EnvVarStreamingIdleTimeoutSeconds]
	require.False(t, set, "out of range values are skipped")
	_, set = env[EnvVarTaskPollMaxFailures]
	require.False(t, set, "a value on a layer the variable lacks is skipped")
	require.Equal(t, 10*time.Minute, env.Duration(EnvVarRelayTimeoutSeconds))
	require.Equal(t, 30*time.Minute, env.Duration(EnvVarTaskTimeoutMinutes))
	require.Equal(t, 16*time.Second, env.Duration(EnvVarResponseHeaderTimeoutSeconds))
	require.Zero(t, env.Duration(EnvVarStreamingIdleTimeoutSeconds))
}

func TestValidateEnvironmentValues(t *testing.T) {
	ctx := context.Background()
	out, fe := ValidateEnvironmentValues(ctx, EnvLayerGroup, "environment", EnvironmentValues{EnvVarRelayTimeoutSeconds: 30, EnvVarResponseHeaderTimeoutSeconds: 0})
	require.Empty(t, fe)
	require.Equal(t, EnvironmentValues{EnvVarRelayTimeoutSeconds: 30}, out, "0 follows the next layer and is dropped")

	// The request body limit is a gateway setting, not a variable of any layer.
	_, fe = ValidateEnvironmentValues(ctx, EnvLayerAccount, "environment", EnvironmentValues{"max_request_body_mb": 8, "nope": 1, EnvVarRelayTimeoutSeconds: 86401})
	fields := map[string]bool{}
	for _, f := range fe {
		fields[f.Field] = true
	}
	require.Equal(t, map[string]bool{"environment.max_request_body_mb": true, "environment.nope": true, "environment.relay_timeout_seconds": true}, fields)
}

// protocol.AccountEnvVars is what manifest/check accepts as a declared
// environment default; it must be exactly the account layer declared here.
func TestAccountEnvVarsMatchSDK(t *testing.T) {
	got := map[string]protocol.EnvVarRange{}
	for _, v := range EnvironmentVariables {
		if v.AllowedOn(EnvLayerAccount) {
			got[v.Key] = protocol.EnvVarRange{Min: v.Min, Max: v.Max}
		}
	}
	require.Equal(t, protocol.AccountEnvVars, got)
}
