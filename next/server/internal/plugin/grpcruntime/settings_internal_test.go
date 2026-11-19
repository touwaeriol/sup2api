package grpcruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

func TestHostGetSettings(t *testing.T) {
	ctx := context.Background()
	i, _ := testInstance(t, nil, nil, registry.Grants{})
	h := &hostServer{i: i}
	if _, err := h.GetSettings(ctx, &pluginv1.GetSettingsRequest{Sections: []string{protocol.SettingsSticky}}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no readers: %v", err)
	}
	i.rt.o.Settings = map[string]core.SettingsReader{}
	for _, name := range protocol.SettingsSections {
		i.rt.o.Settings[name] = func(context.Context) (protocol.SettingsValues, error) {
			return protocol.StickySettings{Enabled: name == protocol.SettingsSticky}, nil
		}
	}
	if _, err := h.GetSettings(ctx, &pluginv1.GetSettingsRequest{Sections: []string{protocol.SettingsSticky, "secrets"}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown section: %v", err)
	}

	// No grant needed; asked twice, a section is answered once.
	r, err := h.GetSettings(ctx, &pluginv1.GetSettingsRequest{Sections: []string{protocol.SettingsSticky, protocol.SettingsBilling, protocol.SettingsSticky}})
	if err != nil || len(r.GetSections()) != 2 || r.GetSections()[0].GetName() != protocol.SettingsSticky || r.GetSections()[1].GetName() != protocol.SettingsBilling {
		t.Fatalf("answer %+v %v", r, err)
	}
	s := r.GetSections()[0]
	sum := sha256.Sum256([]byte(s.GetValuesJson()))
	if s.GetValuesJson() != `{"enabled":true,"default_ttl_seconds":0,"keep_on_account_disabled":false}` || s.GetVersion() != hex.EncodeToString(sum[:8]) {
		t.Fatalf("section %+v", s)
	}
	if r.GetSections()[1].GetVersion() == s.GetVersion() {
		t.Fatal("different values share a version")
	}

	all, err := h.GetSettings(ctx, &pluginv1.GetSettingsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range all.GetSections() {
		names = append(names, s.GetName())
	}
	if !slices.Equal(names, protocol.SettingsSections) {
		t.Fatalf("all sections %v", names)
	}

	// Any reader error (the database, a stored value the owner refuses) is
	// UNAVAILABLE: the plugin keeps what it has and reads again later.
	for _, readErr := range []error{errors.New("connection refused"), core.InvalidFields(core.FieldError{Field: "retry_rules", Code: "invalid"})} {
		i.rt.o.Settings[protocol.SettingsSticky] = func(context.Context) (protocol.SettingsValues, error) { return nil, readErr }
		if _, err := h.GetSettings(ctx, &pluginv1.GetSettingsRequest{Sections: []string{protocol.SettingsSticky}}); status.Code(err) != codes.Unavailable {
			t.Fatalf("reader error %v: %v", readErr, err)
		}
	}
}

// settingsPlugin answers SettingsChanged with err and records the sections.
type settingsPlugin struct {
	pluginv1.PluginServiceClient
	err error
	got [][]string
}

func (p *settingsPlugin) SettingsChanged(_ context.Context, in *pluginv1.SettingsChangedRequest, _ ...grpc.CallOption) (*pluginv1.SettingsChangedResponse, error) {
	p.got = append(p.got, in.GetSections())
	return &pluginv1.SettingsChangedResponse{}, p.err
}

// Every plugin of this protocol answers SettingsChanged: an error, whatever
// its code, is a failure.
func TestInstanceSettingsChanged(t *testing.T) {
	ctx := context.Background()
	i, _ := testInstance(t, nil, nil, registry.Grants{})
	sp := &settingsPlugin{}
	p := *i.proc.Load()
	p.plugin = sp
	i.proc.Store(&p)
	if err := i.SettingsChanged(ctx, []string{protocol.SettingsSticky}); err != nil || len(sp.got) != 1 || sp.got[0][0] != protocol.SettingsSticky {
		t.Fatalf("delivered %v %v", sp.got, err)
	}
	for _, code := range []codes.Code{codes.Unimplemented, codes.Internal} {
		sp.err = status.Error(code, "boom")
		if err := i.SettingsChanged(ctx, nil); err == nil {
			t.Fatalf("%s: no failure", code)
		}
	}

	// Background work holding every slot does not hold the call up.
	sp.err = nil
	bg := i.sems[classBackground]
	for range cap(bg) {
		bg <- struct{}{}
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := i.SettingsChanged(short, nil); err != nil {
		t.Fatalf("with the background slots taken: %v", err)
	}
}

func (p *settingsPlugin) ValidateSettings(_ context.Context, in *pluginv1.ValidateSettingsRequest, _ ...grpc.CallOption) (*pluginv1.ValidateSettingsResponse, error) {
	got := []string{in.GetConfigJson()}
	for _, g := range in.GetGrants() {
		got = append(got, g.GetPermission()+"="+g.GetScopeJson())
	}
	p.got = append(p.got, got)
	return &pluginv1.ValidateSettingsResponse{Errors: []*pluginv1.FieldError{{Field: "a"}}}, p.err
}

// ValidateSettings reaches the plugin's PluginService; its status keeps its
// code through the core error, so UNIMPLEMENTED (an older plugin, or one
// without SettingsValidator) is told apart. The plugin's grants go with it,
// as with Configure.
func TestInstanceValidateSettings(t *testing.T) {
	ctx := context.Background()
	i, _ := testInstance(t, nil, nil, registry.Grants{"net": json.RawMessage(`{"domains":["a.test"]}`)})
	sp := &settingsPlugin{}
	p := *i.proc.Load()
	p.plugin = sp
	i.proc.Store(&p)
	resp, err := i.SettingsValidator().ValidateSettings(ctx, &pluginv1.ValidateSettingsRequest{ConfigJson: `{"a":1}`})
	if err != nil || len(resp.GetErrors()) != 1 || len(sp.got) != 1 || !slices.Equal(sp.got[0], []string{`{"a":1}`, `net={"domains":["a.test"]}`}) {
		t.Fatalf("validate = %v %v %v", resp, err, sp.got)
	}
	sp.err = status.Error(codes.Unimplemented, "no")
	if _, err := i.SettingsValidator().ValidateSettings(ctx, &pluginv1.ValidateSettingsRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unimplemented = %v", err)
	}
}
