package provider

import (
	"strings"
)

// FailureKind is the stable, user-facing reason a model request attempt failed.
// It is derived once from the transport/provider error so the UI, the event
// stream and structured logs never disagree about the cause.
type FailureKind string

const (
	FailureKindRateLimit     FailureKind = "rate_limit"
	FailureKindServerError   FailureKind = "server_error"
	FailureKindAuth          FailureKind = "auth"
	FailureKindQuota         FailureKind = "quota"
	FailureKindTimeout       FailureKind = "timeout"
	FailureKindNetwork       FailureKind = "network"
	FailureKindStreamCut     FailureKind = "stream_interrupted"
	FailureKindEmptyResponse FailureKind = "empty_response"
	FailureKindContextLimit  FailureKind = "context_overflow"
	FailureKindOutputLimit   FailureKind = "output_limit"
	FailureKindProtocol      FailureKind = "protocol_replay"
	// FailureKindClientError covers non-auth 4xx responses: the endpoint
	// rejected the request itself (400/405/413/422, …). Retrying cannot help.
	FailureKindClientError FailureKind = "client_error"
	FailureKindUnknown     FailureKind = "unknown"
)

// AttemptFailure describes one failed model request attempt. It deliberately
// carries classification only: never a response body, request query, or
// credential. Timing and attempt-budget fields are filled by the caller.
type AttemptFailure struct {
	Attempt   int         `json:"attempt"`
	Max       int         `json:"max"`
	Kind      FailureKind `json:"kind"`
	Phase     string      `json:"phase,omitempty"`
	Status    int         `json:"status,omitempty"`
	Code      string      `json:"code,omitempty"`
	Retryable bool        `json:"retryable"`
	BackoffMs int         `json:"backoffMs,omitempty"`
	// RetryAfterMs is the server's raw Retry-After request. It is kept separate
	// from BackoffMs so the UI can say what the server asked for even when the
	// applied delay differs (the schedule was longer, or the cap refused it).
	RetryAfterMs int    `json:"retryAfterMs,omitempty"`
	ElapsedMs    int64  `json:"elapsedMs,omitempty"`
	At           int64  `json:"at,omitempty"`
	ProviderID   string `json:"providerId,omitempty"`
	Model        string `json:"model,omitempty"`
	TraceID      string `json:"traceId,omitempty"`
}

// ExplainFailure classifies err into a display-safe AttemptFailure. It reuses
// ClassifyRecovery so transport classification stays single-sourced. A context
// deadline is intentionally left as unknown: it is a local cancel/budget event,
// not a provider timeout, and must not surface as a retry reason.
func ExplainFailure(err error) AttemptFailure {
	f := ClassifyRecovery(err)
	diag := DiagnoseFailure(err)
	out := AttemptFailure{
		Phase:      f.Phase,
		Status:     f.Status,
		Code:       sanitizeFailureToken(f.Code, 64),
		Retryable:  f.Retryable,
		ProviderID: sanitizeFailureToken(diag.ProviderID, 128),
		TraceID:    sanitizeFailureToken(diag.TraceID, 128),
	}
	if out.Status == 0 {
		out.Status = diag.Status
	}

	switch f.Phase {
	case "quota":
		out.Kind = FailureKindQuota
		out.Retryable = false
	case "auth":
		out.Kind = FailureKindAuth
		out.Retryable = false
	case "limit":
		switch {
		case AsContextLimitError(err) != nil:
			out.Kind, out.Retryable = FailureKindContextLimit, true
		case AsOutputLimitError(err) != nil:
			out.Kind, out.Retryable = FailureKindOutputLimit, true
		default:
			out.Kind = FailureKindUnknown
		}
	case "protocol":
		out.Kind, out.Retryable = FailureKindProtocol, true
	case "empty":
		out.Kind, out.Retryable = FailureKindEmptyResponse, true
	case "stream":
		reason := StreamInterruptReason(err)
		if out.Code == "" {
			out.Code = sanitizeFailureToken(reason, 64)
		}
		switch reason {
		case StreamInterruptIdleTimeout:
			out.Kind = FailureKindTimeout
		case StreamInterruptConnectionReset:
			out.Kind = FailureKindNetwork
		default:
			out.Kind = FailureKindStreamCut
		}
		out.Retryable = true
	case "connect":
		out.Kind, out.Retryable = FailureKindNetwork, true
	case "headers":
		switch {
		case out.Status == 429:
			out.Kind = FailureKindRateLimit
		case out.Status == 408:
			out.Kind = FailureKindTimeout
		case out.Status >= 500 && out.Status <= 599:
			out.Kind = FailureKindServerError
		case out.Status == 401 || out.Status == 403:
			// Auth normally arrives as *AuthError; keep raw header errors
			// classified the same way.
			out.Kind, out.Retryable = FailureKindAuth, false
		case out.Status >= 400 && out.Status <= 499:
			out.Kind, out.Retryable = FailureKindClientError, false
		default:
			out.Kind = FailureKindUnknown
		}
	}
	if out.Kind == "" {
		out.Kind = FailureKindUnknown
	}
	if out.Phase == "" {
		out.Phase = "unknown"
	}
	return out
}

// sanitizeFailureToken keeps only identifiers an operator can safely paste into
// a bug report. Anything else (spaces, quotes, control characters) is dropped
// rather than escaped, because these fields are surfaced verbatim in the UI.
func sanitizeFailureToken(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"sk-", "bearer", "authorization", "api_key", "apikey", "token=", "secret"} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		}
		if b.Len() >= limit {
			break
		}
	}
	return b.String()
}
