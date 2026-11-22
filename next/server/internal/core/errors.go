// Package core holds the types and interfaces shared across server modules.
// It is the integration contract between module owners: modules depend on
// these interfaces, never on each other's concrete types.
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Error is the only error type handlers render to clients. Anything else is
// reported as "internal".
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause returns a copy carrying an internal cause (never rendered).
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.cause = err
	return &c
}

// WithDetails returns a copy with details merged in.
func (e *Error) WithDetails(kv map[string]any) *Error {
	c := *e
	c.Details = make(map[string]any, len(e.Details)+len(kv))
	for k, v := range e.Details {
		c.Details[k] = v
	}
	for k, v := range kv {
		c.Details[k] = v
	}
	return &c
}

// WithMessage returns a copy with a different client-facing message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Standard error codes. Keep in sync with docs/CONTRACTS.md.
var (
	ErrInvalidArgument     = NewError(http.StatusBadRequest, "invalid_argument", "invalid argument")
	ErrUnauthenticated     = NewError(http.StatusUnauthorized, "unauthenticated", "authentication required")
	ErrPermissionDenied    = NewError(http.StatusForbidden, "permission_denied", "permission denied")
	ErrNotFound            = NewError(http.StatusNotFound, "not_found", "not found")
	ErrConflict            = NewError(http.StatusConflict, "conflict", "conflict")
	ErrInsufficientBalance = NewError(http.StatusPaymentRequired, "insufficient_balance", "insufficient balance")
	// ErrKeyQuotaExhausted: the API key spent the quota its owner set (CONTRACTS §61).
	ErrKeyQuotaExhausted  = NewError(http.StatusPaymentRequired, "key_quota_exhausted", "this API key has used up its quota")
	ErrPriceNotConfigured = NewError(http.StatusForbidden, "model_price_not_configured", "model price not configured")
	ErrModelNotFound      = NewError(http.StatusNotFound, "model_not_found", "model not found")
	ErrRateLimited        = NewError(http.StatusTooManyRequests, "rate_limited", "too many requests")
	ErrNoAvailableAccount = NewError(http.StatusServiceUnavailable, "no_available_account", "no available account")
	ErrPluginUnavailable  = NewError(http.StatusServiceUnavailable, "plugin_unavailable", "plugin unavailable")
	ErrUnavailable        = NewError(http.StatusServiceUnavailable, "unavailable", "service unavailable")
	ErrUnsupported        = NewError(http.StatusNotImplemented, "unsupported", "not supported")
	ErrInternal           = NewError(http.StatusInternalServerError, "internal", "internal error")
)

// ChangedMeanwhile is the conflict of a write that lost to a concurrent one
// (store.IsWriteRace): sending it again may succeed.
func ChangedMeanwhile(ctx context.Context, cause error) *Error {
	return ErrConflict.WithMessage(Text(ctx, "the data changed meanwhile, try again", "数据已被其他操作修改，请重试")).WithCause(cause)
}

// FieldError is one validation problem, rendered under details.fields.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// InvalidFields builds an invalid_argument error listing field problems.
func InvalidFields(fields ...FieldError) *Error {
	return ErrInvalidArgument.WithDetails(map[string]any{"fields": fields})
}

// AsError extracts *Error from err, mapping anything else to ErrInternal
// (with the original error kept as cause for logging).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return ErrInternal.WithCause(err)
}
