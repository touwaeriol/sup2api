package conformance

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// declared is what the suite reads from the plugin's manifest: its account
// types, and the platforms their protocols belong to (the built-in ones,
// the manifest's own and Plugin.Platforms).
type declared struct {
	m         manifest.Manifest
	platforms []manifest.Platform
}

func readManifest(raw []byte, others []manifest.Platform) (*declared, error) {
	if len(raw) == 0 {
		return nil, errors.New("Plugin.Manifest is required: the plugin's manifest.json")
	}
	d := &declared{}
	if err := json.Unmarshal(raw, &d.m); err != nil {
		return nil, fmt.Errorf("Plugin.Manifest: %w", err)
	}
	d.platforms = append(append(platforms.Builtin(), d.m.Platforms...), others...)
	return d, nil
}

// balanceDeclared reports whether accountType declares a balance.
func (d *declared) balanceDeclared(accountType string) bool {
	return slices.ContainsFunc(d.m.AccountTypes, func(at manifest.AccountType) bool { return at.ID == accountType && at.Balance != nil })
}

// platformOf is the platform with an endpoint of protocol, nil for none.
func (d *declared) platformOf(protocol string) *manifest.Platform {
	for i := range d.platforms {
		for _, e := range d.platforms[i].Endpoints {
			if e.Protocol == protocol {
				return &d.platforms[i]
			}
		}
	}
	return nil
}

// clientHeaders are the client headers the core hands over with a request
// of protocol on an account of accountType: the passHeaders of the type's
// entry for the protocol's platform, else the platform's
// (manifest.AccountPlatform.ClientHeaders, as the gateway filters them).
func (d *declared) clientHeaders(accountType, protocol string) ([]string, error) {
	p := d.platformOf(protocol)
	if p == nil {
		return nil, fmt.Errorf("protocol %q belongs to no endpoint of a built-in platform or of the manifest", protocol)
	}
	for _, at := range d.m.AccountTypes {
		if at.ID != accountType {
			continue
		}
		for _, ap := range at.Platforms {
			if ap.Platform == p.ID {
				return ap.ClientHeaders(p), nil
			}
		}
		return nil, fmt.Errorf("account type %q of the manifest does not serve platform %s (protocol %q)", accountType, p.ID, protocol)
	}
	return nil, fmt.Errorf("the manifest declares no account type %q", accountType)
}

// requestForms are the forms the core sends a request of a protocol in, read from
// the endpoints of the protocol: an HTTP endpoint with request.stream
// streams only, one with request.streamPath (or a model the plugin
// resolves, which answers the stream too) has both forms, any other none
// but the non-stream one; a websocket endpoint has turns (rule 11).
type requestForms struct {
	known, stream, nonStream, websocket bool
}

func (d *declared) forms(protocol string) requestForms {
	var f requestForms
	for _, p := range d.platforms {
		for _, e := range p.Endpoints {
			if e.Protocol != protocol {
				continue
			}
			f.known = true
			switch {
			case e.WebSocket():
				f.websocket = true
			case e.Request.Stream:
				f.stream = true
			case e.Request.StreamPath != "" || e.Request.ModelSource == manifest.ModelSourcePlugin:
				f.stream, f.nonStream = true, true
			default:
				f.nonStream = true
			}
		}
	}
	return f
}

// has reports whether the core sends an HTTP request of the form.
func (f requestForms) has(stream bool) bool {
	if stream {
		return f.stream
	}
	return f.nonStream
}
