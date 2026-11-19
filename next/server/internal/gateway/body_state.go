package gateway

import "github.com/tidwall/gjson"

// bodyRoot is the current body parsed as a gjson root, parsed once per body
// version. gjson keeps a document as a string, so parsing copies it (a
// megabyte for a large request), and the model-reference scan runs once per
// declared rule, before hooks and after them. setBody drops it: a scan of the
// new bytes must not see the old document.
func (c *call) bodyRoot() gjson.Result {
	if !c.docValid {
		c.doc, c.docValid = gjson.ParseBytes(c.body), true
	}
	return c.doc
}
