package manifest

import "strings"

// A plugin's identity is its publisher plus its key (CONTRACTS §137): two
// publishers may each have a plugin keyed "codex". The core stores one
// string for it, the plugin id, wherever it stored the bare key before
// (table and route keys, the table prefix, the database role):
//
//   - a plugin of OfficialPublisher: its key ("codex");
//   - any other publisher's: "_<publisher>__<key>" ("_erio__codex").
//
// An official id starts with a letter and a third-party one with "_", and
// neither a publisher id nor a key holds "__" or ends in "_", so no id's
// TablePrefix starts another's and ParsePluginID reads both parts back.
// People see DisplayID: "codex" or "erio/codex".

// OfficialPublisher is the publisher of the first-party plugins, whose
// plugin ids are their bare keys.
const OfficialPublisher = "sub2api"

// PluginIDMaxLen is the longest plugin id, the width of the core's key
// columns: a third-party plugin's publisher and key together take at most
// PluginIDMaxLen-3 bytes.
const PluginIDMaxLen = 30

const (
	thirdPartyHead = "_"
	idSeparator    = "__"
	displaySep     = "/"
)

// PluginID is the plugin id of publisher's plugin key.
func PluginID(publisher, key string) string {
	if publisher == OfficialPublisher {
		return key
	}
	return thirdPartyHead + publisher + idSeparator + key
}

// ID is the plugin id of m.
func (m *Manifest) ID() string { return PluginID(m.Publisher, m.Key) }

// ParsePluginID splits a plugin id into its publisher and key; ok is false
// for a string that is not one PluginID can make.
func ParsePluginID(id string) (publisher, key string, ok bool) {
	rest, third := strings.CutPrefix(id, thirdPartyHead)
	if !third {
		return OfficialPublisher, id, id != ""
	}
	publisher, key, ok = strings.Cut(rest, idSeparator)
	return publisher, key, ok && publisher != "" && key != ""
}

// DisplayID is how people see a plugin id: the bare key of an official
// plugin, "publisher/key" for any other.
func DisplayID(id string) string {
	publisher, key, ok := ParsePluginID(id)
	if !ok || publisher == OfficialPublisher {
		return id
	}
	return publisher + displaySep + key
}
