package manifest

import (
	"sort"
	"strings"
)

// SecretField is a masked plugin setting (CONTRACTS §75.1). Credentials are
// values the plugin presents to someone else; sensitive fields
// (SettingsSecret.Sensitive) are addresses such as webhook URLs: masked on
// read, but not credentials themselves.
type SecretField struct {
	Path       []string
	Credential bool
}

// Pointer is the field's JSON pointer below the values ("a/b"; "~" and "/" in
// names escaped as in RFC 6901), as settings field errors name it too.
func (f SecretField) Pointer() string {
	parts := make([]string, len(f.Path))
	for i, p := range f.Path {
		parts[i] = pointerEscaper.Replace(p)
	}
	return strings.Join(parts, "/")
}

var (
	pointerEscaper   = strings.NewReplacer("~", "~0", "/", "~1")
	pointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")
)

// MaxSecretDepth bounds a SettingsSecret path: a field may sit at most this
// many properties deep to be masked on its own.
const MaxSecretDepth = 8

// SettingsSecret is one entry of ui.settings.secrets: Path is the setting's
// JSON pointer below the values, written like SecretField.Pointer ("a/b",
// "~0" and "~1" escaped); Sensitive marks an address that is masked but is
// not a credential (CONTRACTS §75.1). An array element cannot be declared on
// its own: a path that meets an array (or any other value that is not an
// object) on its way masks and keeps that value whole, so declare the array
// itself.
type SettingsSecret struct {
	Path      string `json:"path"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// SecretPath splits a SettingsSecret path into its property names. ok is
// false for a path the core cannot mask one by one: empty, with an empty
// name, with a "~" that does not start "~0" or "~1", or deeper than
// MaxSecretDepth.
func SecretPath(pointer string) (path []string, ok bool) {
	path = splitPointer(pointer)
	if len(path) > MaxSecretDepth {
		return nil, false
	}
	for _, token := range strings.Split(pointer, "/") {
		if !escapedName(token) {
			return nil, false
		}
	}
	return path, true
}

// splitPointer splits a JSON pointer below the values into its names, "~1"
// and "~0" decoded.
func splitPointer(pointer string) []string {
	path := strings.Split(pointer, "/")
	for i, p := range path {
		path[i] = pointerUnescaper.Replace(p)
	}
	return path
}

// SecretFields are the masked settings ui.settings.secrets declares, sorted
// by pointer. The manifest check refuses a path SecretPath does not accept, so every
// declared path is taken as written.
func (f Form) SecretFields() []SecretField {
	out := make([]SecretField, 0, len(f.Secrets))
	for _, s := range f.Secrets {
		out = append(out, SecretField{Path: splitPointer(s.Path), Credential: !s.Sensitive})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pointer() < out[j].Pointer() })
	return out
}

// escapedName: a non-empty name whose every "~" starts "~0" or "~1".
func escapedName(token string) bool {
	if token == "" {
		return false
	}
	for i := range len(token) {
		if token[i] == '~' && (i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1')) {
			return false
		}
	}
	return true
}
