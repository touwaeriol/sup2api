package grpcruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// System settings for plugins (CONTRACTS §107).

// SettingsChangedTimeout bounds one PluginService.SettingsChanged call.
const SettingsChangedTimeout = 2 * time.Second

// GetSettings needs no grant, like GetEnvironment: the sections are
// operating parameters every plugin is expected to honour, and none holds a
// secret.
func (h *hostServer) GetSettings(ctx context.Context, in *pluginv1.GetSettingsRequest) (*pluginv1.GetSettingsResponse, error) {
	names := in.GetSections()
	if len(names) == 0 {
		names = protocol.SettingsSections
	}
	if i := slices.IndexFunc(names, func(n string) bool { return !slices.Contains(protocol.SettingsSections, n) }); i >= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "unknown settings section %q", names[i])
	}
	out := &pluginv1.GetSettingsResponse{}
	for _, name := range names {
		if slices.ContainsFunc(out.Sections, func(s *pluginv1.SettingsSection) bool { return s.GetName() == name }) {
			continue
		}
		read := h.i.rt.o.Settings[name]
		if read == nil {
			return nil, status.Error(codes.Unavailable, "settings unavailable")
		}
		v, err := read(ctx)
		if err != nil {
			// Whatever the owner's error (the database, a stored value it
			// cannot use), the plugin can only keep what it has and read
			// again later.
			h.i.log.Warn("plugin settings read failed", "section", name, "err", err)
			return nil, status.Errorf(codes.Unavailable, "%s settings unavailable", name)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode settings")
		}
		sum := sha256.Sum256(raw)
		out.Sections = append(out.Sections, &pluginv1.SettingsSection{Name: name, Version: hex.EncodeToString(sum[:8]), ValuesJson: string(raw)})
	}
	return out, nil
}

// SettingsValidator is the plugin's check of its own settings; every
// process serves PluginService (registry.addSettingsValidator).
func (i *Instance) SettingsValidator() core.SettingsValidator { return settingsAdapter{i} }

type settingsAdapter struct{ i *Instance }

// ValidateSettings runs plugin code for somebody waiting in the console, so
// it takes the console budget like ValidateCredentials. The plugin gets its
// current grants, as Configure does.
func (a settingsAdapter) ValidateSettings(ctx context.Context, in *pluginv1.ValidateSettingsRequest) (*pluginv1.ValidateSettingsResponse, error) {
	req := &pluginv1.ValidateSettingsRequest{ConfigJson: in.GetConfigJson(), Grants: a.i.settings.Load().protoGrants()}
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, pluginOf, pluginv1.PluginServiceClient.ValidateSettings, req)
}

// SettingsChanged tells the plugin process that sections changed. The SDK
// answers it without plugin code, so it takes no call slot (classNoSlot).
func (i *Instance) SettingsChanged(ctx context.Context, sections []string) error {
	return i.call(ctx, classNoSlot, SettingsChangedTimeout, func(ctx context.Context, p *proc) error {
		_, err := p.plugin.SettingsChanged(ctx, &pluginv1.SettingsChangedRequest{Sections: sections})
		return err
	})
}
