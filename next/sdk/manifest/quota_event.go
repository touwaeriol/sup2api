package manifest

import (
	"time"

	"github.com/tidwall/gjson"
)

// QuotaEvent declares quota windows an upstream reports inside a WebSocket
// message rather than in response headers (Codex's "codex.rate_limits"). The
// host samples every upstream message of a WebSocket session whose top-level
// "type" is Type, with the same observer and window names as the quota
// headers.
type QuotaEvent struct {
	// Type is the message's top-level "type".
	Type string `json:"type"`
	// Windows are read from the message, at most MaxQuotaHeaders.
	Windows []QuotaEventWindow `json:"windows"`
}

// QuotaEventWindow maps fields of a quota event to one window. Values are
// gjson paths in the message with the meaning of the QuotaHeader fields of
// the same names: the window is named by its length (QuotaWindowKey) and the
// reset is in Unix seconds. A field that is absent or does not parse leaves
// that value unknown, and a window none of whose fields is usable is not
// reported.
type QuotaEventWindow struct {
	WindowMinutes   string `json:"windowMinutes"`
	Utilization     string `json:"utilization,omitempty"`
	UtilizationUnit string `json:"utilizationUnit,omitempty"`
	Reset           string `json:"reset,omitempty"`
}

// Event returns the quota event declared for messages of type typ, nil when
// none is (or q is nil).
func (q *AccountQuota) Event(typ string) *QuotaEvent {
	if q == nil || typ == "" {
		return nil
	}
	for i := range q.Events {
		if q.Events[i].Type == typ {
			return &q.Events[i]
		}
	}
	return nil
}

// ReadQuotaEvent applies a quota event declaration to one message: the
// windows are read the way ReadQuotaHeaders reads headers, a path standing
// for a header name.
func ReadQuotaEvent(ev QuotaEvent, msg []byte, now time.Time) []QuotaReading {
	get := func(path string) string { return gjson.GetBytes(msg, path).String() }
	return ReadQuotaHeaders(ev.headers(), get, now)
}

// headers restates the windows as header declarations whose names are the
// paths.
func (ev QuotaEvent) headers() []QuotaHeader {
	out := make([]QuotaHeader, len(ev.Windows))
	for i, w := range ev.Windows {
		out[i] = QuotaHeader{WindowMinutes: w.WindowMinutes, Utilization: w.Utilization,
			UtilizationUnit: w.UtilizationUnit, Reset: w.Reset}
	}
	return out
}
