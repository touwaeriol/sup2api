//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The plugin connection as grpcruntime sets it up (AutoMTLS, the socket
// directory inside a private run directory, a RunnerFunc) with the real
// launcher and Landlock: the core calls the plugin, and the plugin calls the
// core back on the broker socket the core created (what HostService and the
// egress tunnel use). With Landlock ABI 9 that connect is the one the
// RESOLVE_UNIX rule must allow.
func TestLandlockGoPluginRoundTrip(t *testing.T) {
	kernelABI(t)
	exe, _ := os.Executable()
	root, err := os.MkdirTemp("", "gp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	l := NewLauncher(LauncherOptions{Executable: exe, Landlock: true})
	l.wrap = true
	cmd, err := l.Command(context.Background(), core.LaunchSpec{
		PluginKey: "gp", Version: "1.0.0", BinaryPath: staticPlugin(t), RunDir: root, StrictNetwork: true, Seccomp: true,
		Env: []string{"SANDBOX_TEST_CHILD=goplugin", "TMPDIR=" + root},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  goPluginHandshake,
		Plugins:          plugin.PluginSet{"probe": &probePlugin{}},
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		SkipHostEnv:      true,
		AutoMTLS:         true,
		StartTimeout:     time.Minute,
		Logger:           hclog.NewNullLogger(),
		UnixSocketConfig: &plugin.UnixSocketConfig{TempDir: root},
		RunnerFunc: func(_ hclog.Logger, spec *exec.Cmd, _ string) (runner.Runner, error) {
			cmd.Env = append(cmd.Env, spec.Env...)
			cmd.Stderr = os.Stderr
			out, err := cmd.StdoutPipe()
			if err != nil {
				return nil, err
			}
			return &testRunner{cmd: cmd, stdout: out}, nil
		},
	})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		t.Fatalf("handshake under Landlock: %v", err)
	}
	raw, err := rpc.Dispense("probe")
	if err != nil {
		t.Fatal(err)
	}
	p := raw.(*probeClient)
	id := p.broker.NextId()
	go p.broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer(opts...)
		healthpb.RegisterHealthServer(s, health.NewServer())
		return s
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var resp wrapperspb.StringValue
	if err := p.conn.Invoke(ctx, "/"+probeService.ServiceName+"/Call", wrapperspb.UInt32(id), &resp); err != nil || resp.GetValue() != "SERVING" {
		t.Fatalf("plugin -> core call over the broker: %q %v", resp.GetValue(), err)
	}
}

var goPluginHandshake = plugin.HandshakeConfig{ProtocolVersion: 1, MagicCookieKey: "SANDBOX_TEST_PLUGIN", MagicCookieValue: "landlock"}

// probePlugin serves sandbox.test.Probe/Call(broker id): it dials the
// core's broker service with that id and returns the core's health.
type probePlugin struct{ plugin.NetRPCUnsupportedPlugin }

type prober interface {
	call(ctx context.Context, id uint32) (string, error)
}

var probeService = grpc.ServiceDesc{
	ServiceName: "sandbox.test.Probe",
	HandlerType: (*prober)(nil),
	Methods: []grpc.MethodDesc{{MethodName: "Call", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var in wrapperspb.UInt32Value
		if err := dec(&in); err != nil {
			return nil, err
		}
		out, err := srv.(prober).call(ctx, in.GetValue())
		return wrapperspb.String(out), err
	}}},
}

type probeClient struct {
	broker *plugin.GRPCBroker
	conn   *grpc.ClientConn
}

func (probePlugin) GRPCServer(b *plugin.GRPCBroker, s *grpc.Server) error {
	s.RegisterService(&probeService, &brokerProbe{broker: b})
	return nil
}

func (probePlugin) GRPCClient(_ context.Context, b *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &probeClient{broker: b, conn: c}, nil
}

type brokerProbe struct{ broker *plugin.GRPCBroker }

func (h *brokerProbe) call(ctx context.Context, id uint32) (string, error) {
	conn, err := h.broker.Dial(id)
	if err != nil {
		return "", fmt.Errorf("dial the core's broker: %w", err)
	}
	defer conn.Close()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return resp.GetStatus().String(), err
}

func goPluginChild() int {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: goPluginHandshake,
		Plugins:         plugin.PluginSet{"probe": &probePlugin{}},
		GRPCServer:      plugin.DefaultGRPCServer,
	})
	return 0
}

// testRunner starts one exec.Cmd for go-plugin (as grpcruntime's cmdRunner).
type testRunner struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

func (r *testRunner) Start(context.Context) error { return r.cmd.Start() }
func (r *testRunner) Wait(context.Context) error  { return r.cmd.Wait() }
func (r *testRunner) Kill(context.Context) error {
	if err := r.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
func (r *testRunner) Stdout() io.ReadCloser                            { return r.stdout }
func (r *testRunner) Stderr() io.ReadCloser                            { return io.NopCloser(eofReader{}) }
func (r *testRunner) Name() string                                     { return r.cmd.Path }
func (r *testRunner) ID() string                                       { return strconv.Itoa(r.cmd.Process.Pid) }
func (r *testRunner) Diagnose(context.Context) string                  { return "" }
func (r *testRunner) PluginToHost(n, a string) (string, string, error) { return n, a, nil }
func (r *testRunner) HostToPlugin(n, a string) (string, string, error) { return n, a, nil }

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }
