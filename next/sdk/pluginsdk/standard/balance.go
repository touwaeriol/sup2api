package standard

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// BuildBalanceRequest describes the GETs of the type's Balance on the
// account's base URL: the type's headers but content-type, and the key
// where the request's Auth (else the type's) says.
func (p *Platform) BuildBalanceRequest(_ context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	t, r, err := p.balance(in.GetAccount())
	if err != nil {
		return nil, err
	}
	out := &pluginv1.BuildBalanceRequestResponse{}
	for _, b := range t.Balance.Requests {
		out.Requests = append(out.Requests, &pluginv1.BalanceRequest{
			Method:  http.MethodGet,
			Url:     upstreamURL(b.Path, false, r),
			Headers: t.getHeaders(t.auth(b.Auth), r),
		})
	}
	return out, nil
}

// ParseBalanceResponse reads the answers with the type's Balance.Read. The
// host hands one answer per request; any other count is read as a
// transient failure, so Read may index the answers.
func (p *Platform) ParseBalanceResponse(_ context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	t, r, err := p.balance(in.GetAccount())
	if err != nil {
		return nil, err
	}
	answers := in.GetAnswers()
	if len(answers) != len(t.Balance.Requests) {
		return &pluginv1.ParseBalanceResponseResponse{Result: transient(fmt.Sprintf("%d answers to %d balance requests", len(answers), len(t.Balance.Requests)))}, nil
	}
	return &pluginv1.ParseBalanceResponseResponse{Result: t.Balance.Read(r, answers)}, nil
}

// balance resolves the account's type, which must have a Balance, and the
// request its paths and Read see.
func (p *Platform) balance(acc *pluginv1.Account) (*Type, *Request, error) {
	t, cfg, err := p.account(acc)
	if err != nil {
		return nil, nil, err
	}
	if t.Balance == nil {
		return nil, nil, status.Error(codes.Unimplemented, "the account type has no balance")
	}
	return t, &Request{Account: acc, APIKey: cfg.APIKey, BaseURL: cfg.BaseURL}, nil
}

// BalanceFailure is the result of an answer that is not a success, nil for
// a 2xx: a transport error, a 429, a 5xx or any other status is
// ERROR_TYPE_TRANSIENT, a 401 or 403 ERROR_TYPE_AUTH_REJECTED, with the
// status and the start of the body as the message.
func BalanceFailure(a *pluginv1.BalanceAnswer) *pluginv1.BalanceResult {
	code := a.GetStatus()
	switch {
	case a.GetTransportError() != "" || code == 0:
		return transient("transport error: " + textutil.Clip(a.GetTransportError(), classify.MaxReasonMessage))
	case code >= 200 && code < 300:
		return nil
	}
	msg := fmt.Sprintf("upstream returned HTTP %d", code)
	if body := strings.TrimSpace(string(a.GetBody())); body != "" {
		msg += ": " + textutil.Clip(body, classify.MaxReasonMessage)
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: msg}
	}
	return transient(msg)
}

// decimalNumber is a plain decimal number: a sign, digits, a fraction.
var decimalNumber = regexp.MustCompile(`^(-?)(\d+)(?:\.(\d+))?$`)

// Micros is the plain decimal number an upstream answered (e.g. "7.25",
// "1250000", "-1"; gjson's String of a JSON number is one) in millionths,
// BalanceResult.amount_micros: the same number in another form, not a
// conversion (CONTRACTS §52). It is exact to the sixth decimal, a further
// one rounded half away from zero; false for anything else or a number
// whose millionths do not fit an int64 (beyond about 9.2 trillion).
func Micros(number string) (int64, bool) {
	m := decimalNumber.FindStringSubmatch(number)
	if m == nil {
		return 0, false
	}
	frac := m[3] + "0000000" // the sixth decimal and the one after
	n, _ := new(big.Int).SetString(m[2]+frac[:6], 10)
	if frac[6] >= '5' {
		n.Add(n, big.NewInt(1))
	}
	if m[1] == "-" {
		n.Neg(n)
	}
	return n.Int64(), n.IsInt64()
}

func transient(msg string) *pluginv1.BalanceResult {
	return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT, ErrorMessage: msg}
}
