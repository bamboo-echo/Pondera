package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// errorBodyReadTimeout bounds how long draining a non-OK response body may
// block. Proxies and gateways under load (502/524 storms) can send headers and
// then stall the body on a half-open connection; http.Client has no Timeout
// and ResponseHeaderTimeout no longer applies once headers arrive, so without
// this deadline error reporting blocks in io.ReadAll indefinitely with no
// user-visible progress — the turn looks frozen until the process is killed
// (#6607). A var, not a const, so tests can shrink it.
var errorBodyReadTimeout = 10 * time.Second

// SendOptions carries the per-request identity used to label failures.
type SendOptions struct {
	Provider            string // stable provider instance id
	ProviderDisplayName string // user-editable display label
	Protocol            string // configured wire adapter id
	KeyEnv              string // api_key_env the key is read from, when known
	KeySource           string // human-readable source of KeyEnv, when known
	KeyPresent          bool   // a non-empty key is being sent — separates "rejected" from "missing"
	RetryAuth           bool   // retained for compatibility; authentication failures are terminal
}

// RetryInfo is retained for callers of the retired transport retry callback.
type RetryInfo struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Err     error
}

type RetryNotify func(RetryInfo)

type retryNotifyKey struct{}

type requestAttemptCounterKey struct{}

type requestAttemptCounter struct {
	count atomic.Int64
}

// WithRetryNotify retains compatibility with older callers. HTTP requests no
// longer retry automatically, so the callback is never invoked.
func WithRetryNotify(ctx context.Context, fn RetryNotify) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, retryNotifyKey{}, fn)
}

// WithRequestAttemptCounter returns a context that counts every HTTP request
// SendWithRetry starts. An existing counter is reused so a caller can observe
// attempts even when the provider returns before producing a Usage chunk.
// Provider implementations attach the count to usage, including explicit
// protocol/context repair requests made within the same logical model round.
func WithRequestAttemptCounter(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter); counter != nil {
		return ctx
	}
	return context.WithValue(ctx, requestAttemptCounterKey{}, &requestAttemptCounter{})
}

// WithIndependentRequestAttemptCounter gives an auxiliary call its own usage
// count while preserving cancellation and other context values from its parent.
func WithIndependentRequestAttemptCounter(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestAttemptCounterKey{}, &requestAttemptCounter{})
}

// RequestAttemptCount returns the number of HTTP requests started through
// SendWithRetry for the counter attached to ctx.
func RequestAttemptCount(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter)
	if counter == nil {
		return 0
	}
	return int(counter.count.Load())
}

// ApplyRequestAttemptCount copies the stream's exact HTTP request count into a
// Usage record. Contexts without a counter leave the record unchanged so custom
// providers keep the zero-means-one compatibility contract.
func ApplyRequestAttemptCount(ctx context.Context, usage *Usage) {
	if usage == nil {
		return
	}
	if count := RequestAttemptCount(ctx); count > 0 {
		usage.RequestCount = count
	}
}

// UsageWithRequestAttemptCount returns a copy of usage carrying the exact
// number of HTTP requests observed through ctx. When a provider request fails
// before producing token usage, it returns a request-only Usage record so
// callers can still account for the API calls. If neither usage nor attempts
// exist, it returns nil.
func UsageWithRequestAttemptCount(ctx context.Context, usage *Usage) *Usage {
	count := RequestAttemptCount(ctx)
	if usage == nil {
		if count <= 0 {
			return nil
		}
		return &Usage{RequestCount: count, Unknown: true}
	}
	result := *usage
	if count > 0 {
		result.RequestCount = count
	}
	return &result
}

func recordRequestAttempt(ctx context.Context) {
	if ctx == nil {
		return
	}
	counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter)
	if counter != nil {
		counter.count.Add(1)
	}
}

// APIError reports a non-OK HTTP status that isn't an auth failure. Status
// carries the code so the display layer can map it to an actionable, localized
// message; Body is a trimmed snippet of the response.
type APIError struct {
	RetryAfter          time.Duration // uncapped server delay for managed recovery
	ShouldRetry         string        // explicit provider retry hint
	Provider            string        // stable provider instance id
	ProviderDisplayName string
	Protocol            string
	Status              int
	Body                string
	TraceID             string // provider trace identifier from the response headers, when present
	RequestPath         string // path only; query and URL userinfo are never retained
	ToolContext         string // resolved Pondera/MCP identity for provider-indexed tool schema errors
}

func (e *APIError) Error() string {
	label := ProviderDisplayLabel(e.Provider, e.ProviderDisplayName, e.Protocol)
	base := fmt.Sprintf("%s: status %d", label, e.Status)
	switch detail := readableErrorDetail(e.Body); {
	case detail != "":
		base += ": " + detail
	case strings.TrimSpace(e.Body) != "":
		if json.Valid([]byte(strings.TrimSpace(e.Body))) {
			base += "（响应缺少错误详情）"
		} else {
			base += "（响应非 JSON）"
		}
	}
	if e.ToolContext != "" {
		return base + "\n" + e.ToolContext
	}
	return base
}

// readableErrorDetail extracts only the whitelisted structured error fields.
// The raw response body is never surfaced: HTML pages, trace dumps and echoed
// credentials must not reach a user-visible message.
const maxErrorDetailRunes = 160

func readableErrorDetail(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return ""
	}
	var parsed struct {
		Error struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(trimmed), &parsed) != nil {
		return ""
	}
	code := ""
	if len(parsed.Error.Code) > 0 {
		var asString string
		if json.Unmarshal(parsed.Error.Code, &asString) == nil {
			code = asString
		} else {
			code = strings.Trim(string(parsed.Error.Code), `"`)
		}
	}
	for _, field := range []string{parsed.Error.Message, parsed.Error.Type, code} {
		if detail := displaySafeErrorText(field); detail != "" {
			return detail
		}
	}
	return ""
}

// displaySafeErrorText strips markup/control characters, drops anything that
// looks like a credential, and hard-truncates the remainder.
func displaySafeErrorText(value string) string {
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
	inTag := false
	runes := 0
	for _, r := range value {
		switch {
		case r == '<':
			inTag = true
			continue
		case r == '>':
			inTag = false
			continue
		case inTag:
			continue
		case r < 0x20 || r == 0x7f:
			if r != '\t' && r != '\n' && r != '\r' {
				continue
			}
		}
		if runes >= maxErrorDetailRunes {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		runes++
	}
	text := strings.Join(strings.Fields(b.String()), " ")
	return strings.TrimSpace(text)
}

// RetryableStatus reports whether a backoff can plausibly recover from status s:
// 408 (request timeout), 429 (rate limit) and 5xx (incl. Anthropic's 529). Other
// 4xx (400/401/402/422, …) are caller/config problems retrying can't fix.
func RetryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

// IsConnReset distinguishes connection failures (peer reset, truncated body,
// closed socket) from protocol or caller errors for failure reporting. A common
// trigger is a proxy idle-closing SSE during a reasoner's first-token gap.
func IsConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrNonStreamingResponse) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	// net.Error alone does not prove a network failure: url.Error wraps every
	// HTTP client error, and filesystem errors/syscall.Errno can implement it.
	// Require a socket/DNS cause or an actual timeout to label a network failure.
	var op *net.OpError
	var dns *net.DNSError
	if errors.As(err, &op) || errors.As(err, &dns) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	// RFC 9110 also allows an HTTP-date; gateways in front of rate-limited
	// backends use it more often than the delta-seconds form.
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// readErrorBody drains a non-OK response body under a hard deadline and
// returns up to the first 4 KiB for the error message. Context cancellation
// already unblocks the read (the transport aborts body reads when the request
// context is canceled); the timer covers the case nobody cancels — a half-open
// upstream that sent headers and then went silent. Closing the body from the
// timer goroutine is the documented way to unblock an in-flight Read; it
// tears down the connection, which is the right call for a stalled peer.
func readErrorBody(resp *http.Response) []byte {
	timer := time.AfterFunc(errorBodyReadTimeout, func() { resp.Body.Close() })
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// Drain the rest so a healthy connection can be reused; the timer still
	// arms this read, so a body that stalls after the first 4 KiB cannot
	// wedge the retry loop either.
	_, _ = io.Copy(io.Discard, resp.Body)
	timer.Stop()
	resp.Body.Close()
	return msg
}

// SendWithRetry retains its historical name but sends one HTTP request, plus a
// single resend on a fresh connection when a pooled idle connection closed
// before answering (see staleIdleConnection). Every other failure returns to
// the caller without backoff; the user decides whether to try again.
func SendWithRetry(ctx context.Context, httpClient *http.Client, opts SendOptions, newReq func(context.Context) (*http.Request, error)) (*http.Response, error) {
	identity := RequestIdentity{Provider: opts.Provider, DisplayName: opts.ProviderDisplayName, Protocol: opts.Protocol}
	resp, stale, err := sendAttempt(ctx, httpClient, newReq)
	if stale {
		httpClient.CloseIdleConnections()
		resp, _, err = sendAttempt(ctx, httpClient, newReq)
	}
	var build *buildRequestError
	if errors.As(err, &build) {
		return nil, &RequestFailure{Identity: identity, Operation: "build request", Err: build.err}
	}
	if err != nil {
		return nil, &RequestFailure{Identity: identity, Operation: "request failed", Err: err}
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	msg := readErrorBody(resp)
	if quota := QuotaErrorFromResponseWithIdentity(opts.Provider, opts.ProviderDisplayName, opts.Protocol, resp.StatusCode, string(msg)); quota != nil {
		return nil, quota
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{Provider: opts.Provider, ProviderDisplayName: opts.ProviderDisplayName, Protocol: opts.Protocol, KeyEnv: opts.KeyEnv, KeySource: opts.KeySource, Status: resp.StatusCode, HasKey: opts.KeyPresent, Body: strings.TrimSpace(string(msg))}
	}
	apiErr := &APIError{
		RetryAfter: parseRetryAfter(resp), ShouldRetry: resp.Header.Get("x-should-retry"),
		Provider: opts.Provider, ProviderDisplayName: opts.ProviderDisplayName, Protocol: opts.Protocol,
		Status: resp.StatusCode, Body: strings.TrimSpace(string(msg)),
		TraceID: responseTraceID(resp.Header), RequestPath: responseRequestPath(resp),
	}
	if !RetryableStatus(resp.StatusCode) {
		if limitErr := ParseOutputLimitError(apiErr); limitErr != nil {
			return nil, limitErr
		}
		if limitErr := ParseContextLimitError(apiErr); limitErr != nil {
			return nil, limitErr
		}
		if replayErr := ParseReasoningReplayError(apiErr); replayErr != nil {
			return nil, replayErr
		}
	}
	return nil, apiErr
}

type buildRequestError struct{ err error }

func (e *buildRequestError) Error() string { return e.err.Error() }
func (e *buildRequestError) Unwrap() error { return e.err }

// sendAttempt issues one counted, observed request. stale reports that it
// failed on a dead pooled idle connection.
func sendAttempt(ctx context.Context, httpClient *http.Client, newReq func(context.Context) (*http.Request, error)) (resp *http.Response, stale bool, err error) {
	requestCtx, observation := observeRequest(ctx)
	probe := &connectionProbe{}
	req, err := newReq(probe.attach(requestCtx))
	if err != nil {
		observation.finish(err, "build_error")
		return nil, false, &buildRequestError{err: err}
	}
	observation.request(req)
	recordRequestAttempt(ctx)
	resp, err = httpClient.Do(req)
	if err != nil {
		observation.finish(err, "request_error")
		return nil, probe.staleIdleConnection(ctx, err), err
	}
	observation.response(resp)
	return resp, false, nil
}

func responseRequestPath(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	path := resp.Request.URL.EscapedPath()
	if len(path) > 512 {
		return path[:512]
	}
	return path
}

func responseTraceID(header http.Header) string {
	for _, name := range []string{"trace_id", "trace-id", "x-trace-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
