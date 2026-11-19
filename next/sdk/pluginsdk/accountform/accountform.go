// Package accountform is the scaffolding of a plugin's own account form:
// decoding the credentials and settings of a ValidateCredentials request or
// of a stored account, reading their fields, reporting a field error and
// answering with what validation normalized.
//
// The api_key + base_url form has its whole implementation in apikey.Spec;
// this package serves the account types whose fields are their own (cloud
// credentials, key files).
package accountform

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// Object is a decoded JSON object. Numbers are kept as written
// (json.Number), so re-encoding an object changes none of them (a token
// expiry, a large integer).
type Object map[string]any

// Decode decodes a JSON object; blank input and null are an empty one.
func Decode(raw string) (Object, error) {
	o := Object{}
	if strings.TrimSpace(raw) == "" {
		return o, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&o); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	if o == nil {
		o = Object{}
	}
	return o, nil
}

// Str is the trimmed string value of k, "" when absent or not a string.
func (o Object) Str(k string) string {
	s, _ := o[k].(string)
	return strings.TrimSpace(s)
}

// JSON is o encoded.
func (o Object) JSON() []byte {
	b, _ := json.Marshal(o)
	return b
}

// FieldError is one validation problem of a form field.
func FieldError(field, code, msg string) *pluginv1.FieldError {
	return &pluginv1.FieldError{Field: field, Code: code, Message: msg}
}

// Invalid is the ValidateCredentials answer refusing a form for e.
func Invalid(e *pluginv1.FieldError) *pluginv1.ValidateCredentialsResponse {
	return &pluginv1.ValidateCredentialsResponse{Errors: []*pluginv1.FieldError{e}}
}

// Form is the submitted credentials and settings of a ValidateCredentials
// request. Validation may fill in or rewrite fields of both; Normalized
// reports what it changed.
type Form struct {
	Creds, Settings Object
	// The objects as submitted, re-encoded.
	creds, settings []byte
}

// DecodeForm decodes the credentials and settings of in. When one of them is
// not a JSON object it returns the answer refusing the form instead.
func DecodeForm(in *pluginv1.ValidateCredentialsRequest) (*Form, *pluginv1.ValidateCredentialsResponse) {
	creds, err := Decode(in.GetCredentialsJson())
	if err != nil {
		return nil, Invalid(FieldError("", "invalid", "credentials must be a JSON object / 凭证必须是 JSON 对象"))
	}
	settings, err := Decode(in.GetSettingsJson())
	if err != nil {
		return nil, Invalid(FieldError("", "invalid", "settings must be a JSON object / 设置必须是 JSON 对象"))
	}
	return &Form{Creds: creds, Settings: settings, creds: creds.JSON(), settings: settings.JSON()}, nil
}

// Only is the answer refusing the form when its credentials or settings hold
// a key other than keys (code "unknown"), nil when they hold none. Call it
// before validating the fields: keys a plugin keeps without showing them
// (tokens it refreshed) belong in keys too.
func (f *Form) Only(keys ...string) *pluginv1.ValidateCredentialsResponse {
	if errs := fieldcheck.Unknown(keys, f.Creds, f.Settings); len(errs) > 0 {
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}
	return nil
}

// Normalized is the answer accepting the form: the JSON of each object
// validation changed, "" (the core keeps what was submitted) for the others.
func (f *Form) Normalized() *pluginv1.ValidateCredentialsResponse {
	return &pluginv1.ValidateCredentialsResponse{
		NormalizedCredentialsJson: changed(f.creds, f.Creds),
		NormalizedSettingsJson:    changed(f.settings, f.Settings),
	}
}

// changed is o's JSON when it differs from before, else "".
func changed(before []byte, o Object) string {
	after := o.JSON()
	if bytes.Equal(before, after) {
		return ""
	}
	return string(after)
}

// DecodeAccount decodes the credentials and settings of a stored account.
// One that is not a JSON object answers INVALID_ARGUMENT.
func DecodeAccount(acc *pluginv1.Account) (creds, settings Object, err error) {
	if creds, err = Decode(acc.GetCredentialsJson()); err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "account %d: credentials are not a JSON object", acc.GetId())
	}
	if settings, err = Decode(acc.GetSettingsJson()); err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "account %d: settings are not a JSON object", acc.GetId())
	}
	return creds, settings, nil
}
