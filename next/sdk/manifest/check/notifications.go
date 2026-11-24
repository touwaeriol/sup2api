package check

import (
	"fmt"
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// MaxNotificationChannels bounds the channels one plugin declares.
const MaxNotificationChannels = 8

func (v *validator) notifications() {
	n := v.m.Notifications
	if n == nil {
		if slices.ContainsFunc(v.m.Capabilities, func(c manifest.Capability) bool { return c.ID == manifest.CapNotifyChannel }) {
			v.add("notifications", "required", "notify.channel.v1 requires notifications")
		}
		return
	}
	v.needCap("notifications", manifest.CapNotifyChannel)
	if len(n.Channels) == 0 || len(n.Channels) > MaxNotificationChannels {
		v.add("notifications.channels", "invalid", "declare between one and %d channels", MaxNotificationChannels)
	}
	seen := map[string]bool{}
	for i, c := range n.Channels {
		f := fmt.Sprintf("notifications.channels[%d]", i)
		if !keyRe.MatchString(c.ID) {
			v.add(f+".id", "invalid_format", "channel id %q must match %s", c.ID, keyRe.String())
		} else if seen[c.ID] {
			v.add(f+".id", "duplicate", "channel %q declared twice", c.ID)
		} else if i := slices.IndexFunc(v.opt.OtherChannels, func(o ChannelOwner) bool { return o.PluginID != v.m.ID() && o.ID == c.ID }); i >= 0 {
			v.add(f+".id", "channel_conflict", "notification channel %q is already declared by plugin %q", c.ID, manifest.DisplayID(v.opt.OtherChannels[i].PluginID))
		}
		seen[c.ID] = true
		if firstText(c.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
		if c.Recipient != "" && c.Recipient != manifest.RecipientEmail && c.Recipient != manifest.RecipientText {
			v.add(f+".recipient", "invalid", "recipient must be %q or %q", manifest.RecipientEmail, manifest.RecipientText)
		}
	}
}
