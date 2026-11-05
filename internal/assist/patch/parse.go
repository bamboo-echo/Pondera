// Package patch parses and validates the assistant's restricted patch
// language. The model may only produce an envelope with up to ten scalar
// operations; whole-file TOML/JSON, paths and commands are rejected here before
// any configuration write is attempted.
package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxOps bounds one envelope so a single confirmation cannot rewrite a whole
// file by brute force.
const MaxOps = 10

// MaxIntentRunes bounds the human-readable intent line.
const MaxIntentRunes = 200

// Envelope is the only accepted model output shape.
type Envelope struct {
	Version int    `json:"version"`
	Intent  string `json:"intent"`
	Reply   string `json:"reply"`
	Ops     []Op   `json:"ops"`
}

// Op is one restricted operation.
type Op struct {
	Op        string          `json:"op"`
	Path      string          `json:"path,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	Field     string          `json:"field,omitempty"`
	APIKeyEnv string          `json:"api_key_env,omitempty"`
	Value     json.RawMessage `json:"value"`
}

// Op names accepted in the MVP.
const (
	OpSet              = "set"
	OpSetProviderField = "set_provider_field"
	OpSetSecret        = "set_secret"
	OpAddProvider      = "add_provider"
	OpRemoveProvider   = "remove_provider"
)

var credentialKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Rejection carries a machine-readable reason plus a readable message. The
// assistant surfaces Reason in diagnostics and Message in the UI.
type Rejection struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (r *Rejection) Error() string { return r.Reason + ": " + r.Message }

func reject(reason, format string, args ...any) *Rejection {
	return &Rejection{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// Parse decodes one strictly-shaped envelope. Unknown envelope or op fields,
// trailing JSON, wrong versions and non-scalar values are all rejected.
func Parse(raw string) (*Envelope, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return nil, reject("invalid_envelope", "不是合法的信封 JSON：%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, reject("invalid_envelope", "信封之后还有多余内容")
	}
	if err := ValidateShape(&env); err != nil {
		return nil, err
	}
	return &env, nil
}

// DecodeOpValue returns the scalar value of an op as a Go string/int64/float64/
// bool. Objects, arrays, null and malformed numbers are rejected.
func DecodeOpValue(raw json.RawMessage, sensitive bool) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, reject("invalid_value", "缺少 value")
	}
	switch trimmed[0] {
	case '{', '[', 'n':
		return nil, reject("invalid_value", "value 必须是标量，不能是对象、数组或 null")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, reject("invalid_value", "value 无法解析：%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, reject("invalid_value", "value 之后还有多余内容")
	}
	switch typed := v.(type) {
	case string:
		if err := checkStringValue(typed, sensitive); err != nil {
			return nil, err
		}
		return typed, nil
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			f, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, reject("invalid_value", "数字无效：%v", text)
			}
			return f, nil
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, reject("invalid_value", "整数无效：%v", text)
		}
		return n, nil
	case bool:
		return typed, nil
	}
	return nil, reject("invalid_value", "value 必须是字符串、数字或布尔值")
}

// checkStringValue rejects whole-document payloads, control characters, paths
// and shell snippets. Sensitive values (credentials) are only checked for
// length and control characters so an API key is not misclassified.
func checkStringValue(s string, sensitive bool) error {
	if utf8.RuneCountInString(s) > 4096 {
		return reject("invalid_value", "字符串过长（上限 4096 字符）")
	}
	if strings.ContainsAny(s, "\r\n\x00") {
		return reject("invalid_value", "字符串不能包含换行或控制字符")
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return reject("invalid_value", "不接受整份 JSON/TOML 片段")
	}
	if sensitive {
		return nil
	}
	if strings.Contains(s, "=") || looksLikeTOMLSection(trimmed) {
		return reject("invalid_value", "不接受整份 TOML 片段")
	}
	if strings.Contains(s, "\\") || strings.Contains(s, "..") {
		return reject("invalid_value", "不接受路径")
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") {
		return reject("invalid_value", "不接受路径")
	}
	for _, marker := range []string{"&&", "||", "$(", "`", ";", "|"} {
		if strings.Contains(s, marker) {
			return reject("invalid_value", "不接受命令或脚本片段")
		}
	}
	// Model references legitimately contain one slash (provider/model); more
	// than one is a path, not a reference.
	if strings.Count(s, "/") > 1 {
		return reject("invalid_value", "不接受路径")
	}
	return nil
}

func looksLikeTOMLSection(s string) bool {
	if !strings.HasPrefix(s, "[") || strings.HasPrefix(s, "[[") {
		return strings.HasPrefix(s, "[[") && strings.Contains(s, "]]")
	}
	if !strings.HasSuffix(s, "]") {
		return false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	return inner != "" && !strings.ContainsAny(inner, " \t=,{}")
}

// ValidateShape checks envelope-level limits and op names/arity. It does not
// consult the live configuration.
func ValidateShape(env *Envelope) error {
	if env == nil {
		return reject("invalid_envelope", "信封为空")
	}
	if env.Version != 1 {
		return reject("invalid_envelope", "只接受 version=1，收到 %d", env.Version)
	}
	if utf8.RuneCountInString(env.Intent) > MaxIntentRunes {
		return reject("invalid_envelope", "intent 超过 %d 字符", MaxIntentRunes)
	}
	if utf8.RuneCountInString(env.Reply) > 4000 {
		return reject("invalid_envelope", "reply 过长")
	}
	if len(env.Ops) > MaxOps {
		return reject("invalid_envelope", "一次最多 %d 个 op，收到 %d", MaxOps, len(env.Ops))
	}
	if len(env.Ops) == 0 && strings.TrimSpace(env.Reply) == "" {
		return reject("invalid_envelope", "没有 op 时必须给出 reply 解释")
	}
	seen := map[string]bool{}
	for i := range env.Ops {
		op := &env.Ops[i]
		switch op.Op {
		case OpSet:
			if strings.TrimSpace(op.Path) == "" || op.Provider != "" || op.Field != "" || op.APIKeyEnv != "" {
				return reject("invalid_op", "第 %d 个 set 需要 path，且不能带 provider/field/api_key_env", i+1)
			}
		case OpSetProviderField:
			if strings.TrimSpace(op.Provider) == "" || strings.TrimSpace(op.Field) == "" || op.Path != "" || op.APIKeyEnv != "" {
				return reject("invalid_op", "第 %d 个 set_provider_field 需要 provider 与 field", i+1)
			}
		case OpSetSecret:
			if strings.TrimSpace(op.APIKeyEnv) == "" || op.Path != "" || op.Provider != "" || op.Field != "" {
				return reject("invalid_op", "第 %d 个 set_secret 需要 api_key_env", i+1)
			}
		case OpAddProvider:
			if strings.TrimSpace(op.Provider) == "" || op.Path != "" || op.Field != "" || op.APIKeyEnv != "" {
				return reject("invalid_op", "第 %d 个 add_provider 需要 provider，且不能带 path/field/api_key_env", i+1)
			}
			if len(bytes.TrimSpace(op.Value)) == 0 {
				return reject("invalid_op", "第 %d 个 add_provider 缺少 provider 描述 value", i+1)
			}
		case OpRemoveProvider:
			if strings.TrimSpace(op.Provider) == "" || op.Path != "" || op.Field != "" || op.APIKeyEnv != "" {
				return reject("invalid_op", "第 %d 个 remove_provider 需要 provider，且不能带 path/field/api_key_env", i+1)
			}
			if len(bytes.TrimSpace(op.Value)) != 0 {
				return reject("invalid_op", "第 %d 个 remove_provider 不能带 value", i+1)
			}
		default:
			return reject("unknown_op", "不支持的操作 %q（MVP 只接受 set / set_provider_field / set_secret / add_provider / remove_provider）", op.Op)
		}
		key := op.Op + "\x00" + op.Path + "\x00" + op.Provider + "\x00" + op.Field + "\x00" + op.APIKeyEnv
		if seen[key] {
			return reject("duplicate_op", "第 %d 个 op 与前面的操作目标重复", i+1)
		}
		seen[key] = true
	}
	return nil
}
