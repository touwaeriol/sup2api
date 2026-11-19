package check

import (
	"reflect"
	"testing"
)

func TestUpstreamFields(t *testing.T) {
	f := "accountTypes[0].upstreamFields"
	for _, tc := range []struct {
		fields []string
		want   map[string]string
	}{
		{nil, map[string]string{}},
		{[]string{}, map[string]string{}},
		{[]string{"base_url", "video_endpoint"}, map[string]string{}},
		{[]string{"base_url", " "}, map[string]string{f + "[1]": "required"}},
		{[]string{"base_url", "base_url"}, map[string]string{f + "[1]": "duplicate"}},
	} {
		m := controlsManifest(nil)
		m.AccountTypes[0].UpstreamFields = tc.fields
		if got := controlCodes(m); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: codes = %v, want %v", tc.fields, got, tc.want)
		}
	}
}
