package volcengine

// The checks of the asset library's create routes beyond "required": the
// limits of what Ark's fields hold, applied to every caller of the route.
// The console's create dialogs (ui/native) apply the same rules while the
// operator types; these are the ones that count.

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// check refuses the values Ark's asset group fields cannot hold (the length
// limits the console form also applies).
func (in createGroupBody) check() pluginsdk.FieldErrors {
	var errs pluginsdk.FieldErrors
	errs = maxChars(errs, "name", in.Name, 128)
	errs = maxChars(errs, "title", in.Title, 256)
	errs = maxChars(errs, "description", in.Description, 1024)
	return maxChars(errs, "group_type", in.GroupType, 64)
}

// maxChars adds a max_length error when value has more than max characters.
func maxChars(errs pluginsdk.FieldErrors, field, value string, max int) pluginsdk.FieldErrors {
	if utf8.RuneCountInString(value) <= max {
		return errs
	}
	return errs.Add(field, "max_length", fmt.Sprintf("%s must be at most %d characters / %s 最多 %d 个字符", field, max, field, max))
}

// assetURLPattern is what Ark can fetch an asset from: an http(s) or TOS
// address without whitespace.
var assetURLPattern = regexp.MustCompile(`^(https?://|tos://)\S+$`)

// check refuses the values Ark's asset fields cannot hold (the limits the
// console form also applies).
func (in createAssetBody) check() pluginsdk.FieldErrors {
	var errs pluginsdk.FieldErrors
	errs = maxChars(errs, "name", in.Name, 128)
	errs = maxChars(errs, "asset_type", in.AssetType, 64)
	if !assetURLPattern.MatchString(in.URL) {
		return errs.Add("url", "pattern", "url must be an http(s):// or tos:// address without spaces / 素材地址须为不含空白的 http(s):// 或 tos:// 地址")
	}
	return maxChars(errs, "url", in.URL, 2048)
}
