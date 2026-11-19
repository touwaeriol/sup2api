// Package reqsign signs an outgoing HTTP request with AWS Signature Version 4
// (SignAWSSigV4). Only plugins sign: the upstream request they build in
// Execute, whose signed headers (AWSSigV4Headers) they hand to the host as
// authoritative headers (UpstreamRequest.headers), and the test request they
// describe.
//
// The signature is made by the vendor's official library, aws-sdk-go-v2's
// aws/signer/v4; the package decides what is signed and builds no service
// client. A wrong signature fails as a 403 from the vendor, not as a crash,
// so it is pinned by golden vectors (sigv4_test.go).
package reqsign

import (
	"fmt"
	"log/slog"
)

// AWSCredentials are the inputs of a SigV4 signature: an access key id and
// its secret, scoped to a region and a service, optionally with a session
// token.
//
// They carry the secret and must stay out of logs and error messages;
// String, GoString and LogValue redact it for the cases where they are
// formatted by accident.
type AWSCredentials struct {
	Region          string
	Service         string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional
}

// String describes the credentials without their secrets.
func (c AWSCredentials) String() string {
	return fmt.Sprintf("reqsign.AWSCredentials{region=%s service=%s access_key_id=%s session_token=%t}",
		c.Region, c.Service, c.AccessKeyID, c.SessionToken != "")
}

// GoString keeps %#v from printing the secrets.
func (c AWSCredentials) GoString() string { return c.String() }

// LogValue keeps slog from printing the secrets.
func (c AWSCredentials) LogValue() slog.Value {
	return slog.GroupValue(slog.String("region", c.Region), slog.String("service", c.Service),
		slog.String("access_key_id", c.AccessKeyID), slog.Bool("session_token", c.SessionToken != ""))
}
