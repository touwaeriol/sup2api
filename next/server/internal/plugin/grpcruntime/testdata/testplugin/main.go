// Command testplugin is a minimal plugin used by the runtime tests. It talks
// to the host with the protocol package and generated services only.
//
// Behaviour switches:
//   - Execute with a body that is not a script exercises HostService (a KV
//     counter, a log line) and relays without waiting; a body
//     {"script":[...],"return":...} runs the steps of script.go, and so does
//     Monitor with the script in entry.ref_id.
//   - HTTP POST /replay repeats ExecuteHTTP with the execution token of the
//     last Execute call, which has returned.
//   - Built with -ldflags "-X main.handshakeProtocol=1" the binary answers
//     the go-plugin handshake with that protocol version, with
//     "-X main.infoProtocol=1" it reports it in GetInfo.
//   - ValidateCredentials with credentials_json containing "crash" exits.
//   - BuildTestRequest with model "sleep:<ms>" sleeps before answering.
//   - BuildBalanceRequest builds a keyed GET of /balance and a public GET of
//     /status, or answers Unimplemented for account type "none";
//     ParseBalanceResponse echoes what it got (see ParseBalanceResponse).
//   - Configure rejects {"reject": true}.
//   - HTTP POST /publish publishes the body as broadcast topic "t.ping";
//     OnBroadcast stores "<topic>:<payload>:<source node>" in KV t/broadcast.
//   - HTTP POST /lock makes one HostService lock call (see lockCall).
//   - HTTP POST /kvbatch makes one HostService kv batch call (see
//     kvBatchCall).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

type server struct {
	pluginv1.UnimplementedPluginServiceServer
	pluginv1.UnimplementedPlatformServiceServer
	pluginv1.UnimplementedHTTPServiceServer
	pluginv1.UnimplementedHookServiceServer
	pluginv1.UnimplementedMigrationServiceServer
	pluginv1.UnimplementedAppServiceServer

	broker *plugin.GRPCBroker
	mu     sync.Mutex
	host   pluginv1.HostServiceClient
	config map[string]any
	grants []string
	token  string // execution token of the last Execute call
}

// Set at build time to stand for a binary of another plugin protocol.
var handshakeProtocol, infoProtocol string

// protocolOr is the protocol version override, or the current version.
func protocolOr(override string) uint {
	if n, err := strconv.Atoi(override); err == nil {
		return uint(n)
	}
	return protocol.ProtocolVersion
}

func (s *server) hostClient() pluginv1.HostServiceClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host
}

// GetInfo names the plugin by its bare key, as a real plugin does: the
// host's SUB2API_PLUGIN_KEY is the plugin id (CONTRACTS §137).
func (s *server) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	_, key, _ := manifest.ParsePluginID(os.Getenv(protocol.EnvPluginKey))
	return &pluginv1.GetInfoResponse{
		PluginKey:       key,
		Version:         os.Getenv(protocol.EnvPluginVersion),
		SdkVersion:      "test",
		ProtocolVersion: int32(protocolOr(infoProtocol)),
		Capabilities:    []string{"platform.adapter.v1", "http.routes.v1", "gateway.hook.v1", "migration.data.v1", "app.broadcast.v1"},
	}, nil
}

func (s *server) InitHost(_ context.Context, in *pluginv1.InitHostRequest) (*pluginv1.InitHostResponse, error) {
	conn, err := s.broker.Dial(in.GetHostBrokerId())
	if err != nil {
		return &pluginv1.InitHostResponse{Ready: false, Message: err.Error()}, nil
	}
	s.mu.Lock()
	s.host = pluginv1.NewHostServiceClient(conn)
	s.mu.Unlock()
	return &pluginv1.InitHostResponse{Ready: true}, nil
}

func (s *server) Configure(_ context.Context, in *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(in.GetConfigJson()), &cfg)
	if cfg["reject"] == true {
		return &pluginv1.ConfigureResponse{Errors: []*pluginv1.FieldError{{Field: "reject", Code: "invalid", Message: "rejected"}}}, nil
	}
	var grants []string
	for _, g := range in.GetGrants() {
		grants = append(grants, g.GetPermission())
	}
	s.mu.Lock()
	s.config, s.grants = cfg, grants
	s.mu.Unlock()
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *server) SettingsChanged(context.Context, *pluginv1.SettingsChangedRequest) (*pluginv1.SettingsChangedResponse, error) {
	return &pluginv1.SettingsChangedResponse{}, nil
}

func (s *server) Health(context.Context, *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	return &pluginv1.HealthResponse{Healthy: true}, nil
}

func (s *server) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
	return &pluginv1.ShutdownResponse{}, nil
}

func (s *server) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	if strings.Contains(in.GetCredentialsJson(), "crash") {
		os.Exit(3)
	}
	return &pluginv1.ValidateCredentialsResponse{}, nil
}

// Execute runs the script in the body, or by default exercises HostService:
// it increments a KV counter and relays, without waiting, a request whose
// headers echo the counter, the configured greeting and the pid.
func (s *server) Execute(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	s.mu.Lock()
	s.token = in.GetExecutionToken()
	s.mu.Unlock()
	if sc, ok := parseScript(in.GetBody()); ok {
		return s.runExecute(ctx, in, sc)
	}
	h := s.hostClient()
	n := 0
	got, err := h.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "t", Key: "calls"})
	if err != nil {
		return nil, err
	}
	if got.GetFound() {
		n, _ = strconv.Atoi(string(got.GetValue()))
	}
	n++
	if _, err := h.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "t", Key: "calls", Value: []byte(strconv.Itoa(n))}); err != nil {
		return nil, err
	}
	_, _ = h.Log(ctx, &pluginv1.LogRequest{Level: pluginv1.LogRequest_LEVEL_INFO, Message: "build", Attrs: map[string]string{"n": strconv.Itoa(n)}})
	s.mu.Lock()
	greeting := fmt.Sprint(s.config["greeting"])
	s.mu.Unlock()
	_, err = h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: in.GetExecutionToken(), Request: &pluginv1.UpstreamRequest{
		Method:  "POST",
		Url:     "https://upstream.example.com/v1/messages",
		Headers: map[string]string{"x-calls": strconv.Itoa(n), "x-greeting": greeting, "x-pid": strconv.Itoa(os.Getpid())},
		Body:    in.GetBody(),
	}})
	if err != nil {
		return nil, err
	}
	return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}, nil
}

func (s *server) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return &pluginv1.ClassifyErrorResponse{Action: pluginv1.ClassifyErrorResponse_ACTION_FAILOVER}, nil
}

func (s *server) BuildModelsRequest(context.Context, *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	return nil, status.Error(codes.Unimplemented, "test plugin lists no models")
}

func (s *server) BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	if ms, ok := strings.CutPrefix(in.GetModel(), "sleep:"); ok {
		d, _ := strconv.Atoi(ms)
		select {
		case <-time.After(time.Duration(d) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &pluginv1.BuildTestRequestResponse{Method: "POST", Url: "https://upstream.example.com/test", BodyJson: "{}"}, nil
}

func (s *server) BuildBalanceRequest(_ context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	if in.GetAccount().GetType() == "none" {
		return nil, status.Error(codes.Unimplemented, "test plugin reads no balance")
	}
	return &pluginv1.BuildBalanceRequestResponse{Requests: []*pluginv1.BalanceRequest{
		{Url: "https://upstream.example.com/balance", Headers: map[string]string{"authorization": "Bearer " + in.GetAccount().GetCredentialsJson()}},
		{Method: "POST", Url: "https://upstream.example.com/status", BodyJson: "{}"},
	}}, nil
}

// ParseBalanceResponse reads the answers: amount_micros is the first
// status times 1000 plus the second, currency the first's x-currency header,
// unlimited a second body "unlimited", and the error message the second's
// transport error.
func (s *server) ParseBalanceResponse(_ context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	a := in.GetAnswers()
	if len(a) != 2 {
		return nil, status.Errorf(codes.InvalidArgument, "%d answers", len(a))
	}
	return &pluginv1.ParseBalanceResponseResponse{Result: &pluginv1.BalanceResult{
		AmountMicros: int64(a[0].GetStatus())*1000 + int64(a[1].GetStatus()), Currency: a[0].GetHeaders()["x-currency"],
		Unlimited: string(a[1].GetBody()) == "unlimited", ErrorMessage: a[1].GetTransportError(),
	}}, nil
}

func (s *server) OnGatewayRequest(_ context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
	if strings.Contains(in.GetFields()["prompt_text"], "bad") {
		return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_DENY, DenyStatus: 403, DenyCode: "blocked"}, nil
	}
	return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_ALLOW}, nil
}

func (s *server) HandleHTTP(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	out := map[string]any{
		"path":    in.GetPath(),
		"route":   in.GetRoutePath(),
		"params":  in.GetPathParams(),
		"user_id": in.GetCaller().GetUserId(),
		"body":    string(in.GetBody()),
		"version": os.Getenv(protocol.EnvPluginVersion),
	}
	_, hasAuth := in.GetHeaders()["authorization"]
	out["saw_authorization"] = hasAuth
	if in.GetCaller().GetUserId() > 0 {
		r, err := s.hostClient().AuthzCheck(ctx, &pluginv1.AuthzCheckRequest{UserId: in.GetCaller().GetUserId(), Permission: "rules:read"})
		if err != nil {
			return nil, err
		}
		out["allowed"] = r.GetAllowed()
	}
	if in.GetPath() == "/dsn" {
		r, err := s.hostClient().GetDSN(ctx, &pluginv1.GetDSNRequest{})
		if err != nil {
			return nil, err
		}
		out["dsn"], out["schema"] = r.GetDsn(), r.GetSchema()
	}
	if in.GetPath() == "/publish" {
		if _, err := s.hostClient().Publish(ctx, &pluginv1.PublishRequest{Topic: "t.ping", Payload: in.GetBody()}); err != nil {
			return nil, err
		}
		out["published"] = true
	}
	if in.GetPath() == "/credit" {
		r, err := s.hostClient().LedgerCredit(ctx, &pluginv1.LedgerChangeRequest{UserId: 1, Amount: string(in.GetBody()), IdempotencyKey: in.GetQuery()["idem"].GetValues()[0]})
		if err != nil {
			return nil, err
		}
		out["ledger_id"], out["balance_after"], out["duplicate"] = r.GetLedgerId(), r.GetBalanceAfter(), r.GetDuplicate()
	}
	if in.GetPath() == "/replay" {
		s.mu.Lock()
		token := s.token
		s.mu.Unlock()
		_, err := s.hostClient().ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: token, Url: string(in.GetBody())})
		out["code"] = status.Code(err).String()
	}
	if in.GetPath() == "/lock" {
		lockCall(ctx, s.hostClient(), in.GetBody(), out)
	}
	if in.GetPath() == "/kvbatch" {
		kvBatchCall(ctx, s.hostClient(), in.GetBody(), out)
	}
	if in.GetPath() == "/env" {
		// The plugin's own environment: names only, plus where its
		// temporary directory and sockets are (isolation tests).
		var names []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			names = append(names, k)
		}
		out["env"] = names
		out["tmpdir"] = os.TempDir()
		out["socket_dir"] = os.Getenv("PLUGIN_UNIX_SOCKET_DIR")
	}
	b, _ := json.Marshal(out)
	return &pluginv1.HTTPResponse{
		Status:  200,
		Headers: map[string]*pluginv1.HeaderValues{"Content-Type": {Values: []string{"application/json"}}, "Set-Cookie": {Values: []string{"x=1"}}},
		Body:    b,
	}, nil
}

func (s *server) MigrateData(ctx context.Context, in *pluginv1.MigrateDataRequest) (*pluginv1.MigrateDataResponse, error) {
	_, err := s.hostClient().KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "t", Key: "migrated", Value: []byte(in.GetFromVersion() + "->" + in.GetToVersion())})
	return &pluginv1.MigrateDataResponse{}, err
}

func (s *server) OnBroadcast(ctx context.Context, in *pluginv1.OnBroadcastRequest) (*pluginv1.OnBroadcastResponse, error) {
	v := in.GetTopic() + ":" + string(in.GetPayload()) + ":" + in.GetSourceNodeId()
	_, err := s.hostClient().KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "t", Key: "broadcast", Value: []byte(v)})
	return &pluginv1.OnBroadcastResponse{}, err
}

// lockCall runs one HostService lock call described by body
// {"op":"acquire|renew|release","name":...,"token":...,"ttl_ms":...} and
// reports the result in out; a host error is reported as out["code"] (the
// gRPC code name) rather than failing the HTTP call, so the test sees the
// exact code the plugin got.
func lockCall(ctx context.Context, h pluginv1.HostServiceClient, body []byte, out map[string]any) {
	var req struct {
		Op    string `json:"op"`
		Name  string `json:"name"`
		Token string `json:"token"`
		TTLMs int64  `json:"ttl_ms"`
	}
	_ = json.Unmarshal(body, &req)
	var err error
	switch req.Op {
	case "acquire":
		var r *pluginv1.LockAcquireResponse
		if r, err = h.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: req.Name, Token: req.Token, TtlMs: req.TTLMs}); err == nil {
			out["acquired"], out["valid_ms"] = r.GetAcquired(), r.GetValidMs()
		}
	case "renew":
		var r *pluginv1.LockRenewResponse
		if r, err = h.LockRenew(ctx, &pluginv1.LockRenewRequest{Name: req.Name, Token: req.Token, TtlMs: req.TTLMs}); err == nil {
			out["held"], out["valid_ms"] = r.GetHeld(), r.GetValidMs()
		}
	case "release":
		_, err = h.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: req.Name, Token: req.Token})
	default:
		err = status.Error(codes.InvalidArgument, "unknown op")
	}
	out["code"] = status.Code(err).String()
}

// kvBatchCall runs one HostService kv batch call described by body
// {"op":"set|get|delete","ns":...,"keys":[...],"size":...,"ttl":...}: set
// writes every key with ttl seconds and a value of size bytes ("v:<key>"
// when size is 0); get reports found (one per key), bytes (the values'
// total) and, for small answers, values. A host error is reported as
// out["code"] and out["message"], like lockCall.
func kvBatchCall(ctx context.Context, h pluginv1.HostServiceClient, body []byte, out map[string]any) {
	var req struct {
		Op   string   `json:"op"`
		NS   string   `json:"ns"`
		Keys []string `json:"keys"`
		Size int      `json:"size"`
		TTL  int64    `json:"ttl"`
	}
	_ = json.Unmarshal(body, &req)
	var err error
	switch req.Op {
	case "set":
		items := make([]*pluginv1.KVSetManyItem, len(req.Keys))
		for i, k := range req.Keys {
			value := []byte("v:" + k)
			if req.Size > 0 {
				value = bytes.Repeat([]byte("v"), req.Size)
			}
			items[i] = &pluginv1.KVSetManyItem{Key: k, Value: value, TtlSeconds: req.TTL}
		}
		_, err = h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: req.NS, Items: items})
	case "get":
		var r *pluginv1.KVGetManyResponse
		if r, err = h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: req.NS, Keys: req.Keys}); err == nil {
			found, values, total := []bool{}, []string{}, 0
			for _, it := range r.GetItems() {
				found = append(found, it.GetFound())
				values = append(values, string(it.GetValue()))
				total += len(it.GetValue())
			}
			out["found"], out["bytes"] = found, total
			if total <= 1024 {
				out["values"] = values
			}
		}
	case "delete":
		_, err = h.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: req.NS, Keys: req.Keys})
	default:
		err = status.Error(codes.InvalidArgument, "unknown op")
	}
	out["code"], out["message"] = status.Code(err).String(), status.Convert(err).Message()
}

func main() {
	s := &server{}
	handshake := protocol.Handshake
	handshake.ProtocolVersion = protocolOr(handshakeProtocol)
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: handshake,
		Plugins: protocol.PluginMap(&protocol.GRPCPlugin{Register: func(g *grpc.Server, broker *plugin.GRPCBroker) error {
			s.broker = broker
			pluginv1.RegisterPluginServiceServer(g, s)
			pluginv1.RegisterPlatformServiceServer(g, s)
			pluginv1.RegisterHTTPServiceServer(g, s)
			pluginv1.RegisterHookServiceServer(g, s)
			pluginv1.RegisterMigrationServiceServer(g, s)
			pluginv1.RegisterAppServiceServer(g, s)
			return nil
		}}),
		// A platform plugin receives whole request bodies: no receive limit
		// below the transport's (the SDK does the same).
		GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
			return grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(math.MaxInt32))...)
		},
	})
}
