package reqsign

// AWS Signature Version 4, header authentication, for services other than S3
// (the canonical path is escaped twice and the payload is always hashed).
//
// The signature is made by the official signer, aws-sdk-go-v2's
// aws/signer/v4: the canonical request, the string to sign, the key
// derivation and the Authorization header are its work.
//
// What this package decides:
//
//   - signed headers: host and x-amz-date, plus content-type and
//     x-amz-security-token when present. The SDK signer signs every header
//     on the request it is handed (but for a short ignore list) and
//     content-length. A request of the gateway also carries what the client
//     sent and what the plugin added, and layers after the signature (header
//     overrides, the proxy, net/http itself) may still change those: signing
//     them would turn any such change into a 403. So the signer is handed a
//     bare copy of the request with only Content-Type on it, and its three
//     result headers are copied back, replacing what an earlier layer put
//     in them;
//   - a query that cannot be parsed is an error: the signer would sign and
//     send it without the parameters net/url drops;
//   - what is sent is what was signed: the query and the host the signer
//     settled on (below) are written back to the request.
//
// What the library does, with its defaults, on the bare copy:
//
//   - canonical path: every segment of the path as it goes on the wire
//     (URL.EscapedPath) is escaped once more with the RFC 3986 unreserved
//     set, so ":" sent as "%3A" is signed as "%253A";
//   - canonical query: each name and value percent-encoded with the
//     unreserved set (a space is "%20", never "+"), then sorted by name and
//     value. It rewrites URL.RawQuery to that string;
//   - host: Request.Host, else URL.Host, without the scheme's default port,
//     set as Request.Host. (It also drops the brackets of an IPv6 literal it
//     takes the default port off; AWS endpoints are names.)

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// sigV4ResultHeaders are the headers the signer writes. Whatever an earlier
// layer put in them is replaced, never signed over or sent next to the new
// values.
var sigV4ResultHeaders = []string{"Authorization", "X-Amz-Date", "X-Amz-Security-Token"}

// sigV4SignedHeaders are what AWSSigV4Headers returns: the result headers
// and the Content-Type the signature covers.
var sigV4SignedHeaders = append([]string{"Content-Type"}, sigV4ResultHeaders...)

// sigV4SignerOptions adjust the SDK signer. Production sets none; the tests
// have the signer log the canonical request it signed (sigv4_test.go).
var sigV4SignerOptions []func(*v4.SignerOptions)

// SignAWSSigV4 signs req in place. payload must be the exact bytes req sends
// as its body (nil or empty for none): it is hashed into the signature, which
// is why it is a parameter rather than read back from req.Body. It must run
// after the URL, the host and every header it signs are final; headers it
// does not sign may still change afterwards.
//
// Credentials it cannot use return an error and leave the request unsigned:
// the caller must not send it. Errors never contain the secret or the
// session token.
func SignAWSSigV4(req *http.Request, payload []byte, c AWSCredentials, now time.Time) error {
	if req == nil || req.URL == nil {
		return errors.New("reqsign: no request to sign")
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" || c.Region == "" || c.Service == "" {
		return errors.New("reqsign: aws-sigv4 needs region, service, access key id and secret access key")
	}
	if _, err := url.ParseQuery(req.URL.RawQuery); err != nil {
		return fmt.Errorf("reqsign: aws-sigv4 cannot parse the query string: %w", err)
	}
	bare := sigV4BareRequest(req)
	creds := aws.Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}
	sum := sha256.Sum256(payload)
	// A signer of its own for every signature: a shared one caches the
	// derived key per region and service under the access key id alone, and
	// would keep signing with the old secret after an account's secret was
	// corrected.
	if err := v4.NewSigner(sigV4SignerOptions...).SignHTTP(context.Background(), creds, bare, hex.EncodeToString(sum[:]), c.Service, c.Region, now); err != nil {
		// The signer's own text is left out: nothing here vouches for what
		// it quotes.
		return errors.New("reqsign: aws-sigv4 signing failed")
	}
	// What goes on the wire is exactly what was signed.
	req.URL.RawQuery, req.Host = bare.URL.RawQuery, bare.Host
	for _, name := range sigV4ResultHeaders {
		req.Header.Del(name)
		if v := bare.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	return nil
}

// AWSSigV4Headers returns the headers of req, signed by SignAWSSigV4, that
// the signature wrote or covers, those present: Authorization, X-Amz-Date,
// X-Amz-Security-Token and Content-Type. A plugin hands them to the host as
// authoritative headers so that no later layer changes them. The host is
// not among them (the URL carries it); every other header of req stays the
// plugin's to classify.
func AWSSigV4Headers(req *http.Request) http.Header {
	out := http.Header{}
	for _, name := range sigV4SignedHeaders {
		if values := req.Header.Values(name); len(values) > 0 {
			out[http.CanonicalHeaderKey(name)] = values
		}
	}
	return out
}

// sigV4BareRequest is the part of req the signature covers besides the
// payload: method, URL, host and Content-Type. The signer works on it, so
// req is untouched until the signature exists.
func sigV4BareRequest(req *http.Request) *http.Request {
	u := *req.URL
	bare := &http.Request{Method: req.Method, URL: &u, Host: req.Host, Header: http.Header{}}
	if ct := req.Header.Values("Content-Type"); len(ct) > 0 {
		bare.Header["Content-Type"] = ct
	}
	return bare
}
