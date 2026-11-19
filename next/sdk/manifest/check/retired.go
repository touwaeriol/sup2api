package check

import (
	"encoding/json"
	"fmt"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// CodeOutdatedContract marks what only a package built for an older plugin
// contract declares: runtime "none", the capabilities platform.execute.v1
// and platform.poll.v1, the manifest fields the contract before the single
// Execute path read (retiredContractFields) and the account host permissions
// (retiredAccountPermissions). The host answers such a package with
// protocol.OutdatedContractMessage instead of the field errors.
const CodeOutdatedContract = "outdated_contract"

// OutdatedContract reports whether err is a manifest validation error that
// includes a CodeOutdatedContract problem.
func OutdatedContract(err error) bool {
	fields, _ := Fields(err)
	for _, f := range fields {
		if f.Code == CodeOutdatedContract {
			return true
		}
	}
	return false
}

// OutdatedPackage decides whether the package of manifest m (manifestJSON
// is its manifest.json as written) is built for an older plugin contract:
// its manifest declares what only that contract did (CodeOutdatedContract).
// If so it returns the one message for it, protocol.OutdatedContractMessage
// with hostVersion, the version of the core that refuses. Upload, install,
// load and the CLI wrap it in their own error; nothing but the manifest is
// read (a binary of an older protocol shows at its handshake).
func OutdatedPackage(m *manifest.Manifest, manifestJSON []byte, hostVersion string) (string, bool) {
	if m == nil || !OutdatedContract(Validate(m, map[string][]byte{"manifest.json": manifestJSON}, ValidateOptions{Tooling: true})) {
		return "", false
	}
	return protocol.OutdatedContractMessage(m.Key, m.Version, hostVersion), true
}

// What the single Execute path removed from the manifest.
const retiredRuntimeNone = "none"

var retiredCapabilities = map[string]bool{
	"platform.execute.v1": true,
	"platform.poll.v1":    true,
}

// retiredAccountPermissions are the host permissions that gated a plugin's
// own accounts: it receives them without a grant now (CONTRACTS §124).
var retiredAccountPermissions = map[string]bool{
	"accounts.credentials": true,
	"accounts.read":        true,
}

// retiredContractFields rejects the manifest fields the single Execute path
// removed, which would otherwise decode to nothing: the declared upstream of
// an account type, the request fields of the build-and-patch calls and the
// per-endpoint body limit (the gateway has one request body limit).
func (v *validator) retiredContractFields() {
	var root rawObject
	if json.Unmarshal(v.files["manifest.json"], &root) != nil {
		return
	}
	retired := func(o rawObject, field, name, why string) {
		if _, ok := o[name]; ok {
			v.add(field+"."+name, CodeOutdatedContract, "%s was removed: %s", name, why)
		}
	}
	const wholeBody = "PlatformService.Execute receives the whole request body"
	for i, p := range children(root, "platforms") {
		f := fmt.Sprintf("platforms[%d]", i)
		retired(p, f, "requestFields", wholeBody)
		for j, e := range children(p, "endpoints") {
			retired(child(e, "request"), fmt.Sprintf("%s.endpoints[%d].request", f, j), "maxBodyBytes", "the request body limit is a gateway setting")
		}
	}
	for i, at := range children(root, "accountTypes") {
		f := fmt.Sprintf("accountTypes[%d]", i)
		retired(at, f, "upstream", "the plugin builds its upstream requests in PlatformService.Execute")
		for j, ap := range children(at, "platforms") {
			retired(ap, fmt.Sprintf("%s.platforms[%d]", f, j), "requestFields", wholeBody)
		}
	}
}

// rawObject is a JSON object as written, for fields the manifest types no
// longer have.
type rawObject = map[string]json.RawMessage

// child decodes the object at o[key]; nil when there is none.
func child(o rawObject, key string) rawObject {
	var out rawObject
	_ = json.Unmarshal(o[key], &out)
	return out
}

// children decodes the array of objects at o[key]; nil when there is none.
func children(o rawObject, key string) []rawObject {
	var out []rawObject
	_ = json.Unmarshal(o[key], &out)
	return out
}
