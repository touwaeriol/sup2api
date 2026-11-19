package standard

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

// The body is edited with jsonedit, in place of the bytes an edit names:
// everything else - key order, white space, unknown fields, escapes,
// numbers - is returned as the core handed it over. It is never decoded
// whole.
//
// The core hands over valid JSON without duplicate keys (it rejects other
// bodies with 400 before any plugin runs). An edited body is checked again
// all the same (jsonedit's rules): a body that is not JSON, or a key an
// edit sets that appears twice in its object, is refused (INVALID_ARGUMENT,
// nothing is relayed) instead of being sent half edited.

// edit deletes the type's FilterFields from body, then runs the route's
// Edit, in one jsonedit pass; with neither the body is not read.
func (t *Type) edit(body []byte, route *Route, r *Request) ([]byte, error) {
	if len(t.FilterFields) == 0 && route.Edit == nil {
		return body, nil
	}
	body, err := jsonedit.Edit(body, func(d *jsonedit.Doc) {
		for _, f := range t.FilterFields {
			d.Delete(f)
		}
		if route.Edit != nil {
			route.Edit(d, r)
		}
	})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "request body: %v", err)
	}
	return body, nil
}
