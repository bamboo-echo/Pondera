package patch

import (
	"fmt"
	"strconv"
	"strings"
)

// DiffEntry is one redacted, human-readable preview row.
type DiffEntry struct {
	Label     string `json:"label"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Sensitive bool   `json:"sensitive"`
	Risk      string `json:"risk"`
}

// RenderDiff turns resolved ops into preview rows. Sensitive values never
// appear: before shows only presence, after shows a fixed redaction marker.
func RenderDiff(ops []ResolvedOp) []DiffEntry {
	out := make([]DiffEntry, 0, len(ops))
	for _, op := range ops {
		entry := DiffEntry{Label: op.Label, Risk: op.Risk, Sensitive: op.Sensitive}
		if op.Sensitive {
			entry.Before = presenceText(op.Before)
			entry.After = "已设置（脱敏，长度 " + strconv.Itoa(len(fmt.Sprint(op.Value))) + "）"
			out = append(out, entry)
			continue
		}
		entry.Before = displayScalar(op.Before)
		entry.After = displayScalar(op.Value)
		out = append(out, entry)
	}
	return out
}

func displayScalar(v any) string {
	switch typed := v.(type) {
	case nil:
		return "(空)"
	case string:
		if strings.TrimSpace(typed) == "" {
			return "(空)"
		}
		return typed
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

func presenceText(v any) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return "未设置"
}

// CommentWarning is shown in the preview because user-scope SaveTo re-renders
// the whole annotated TOML and drops hand-written comments.
const CommentWarning = "保存会按 Pondera 的标准模板重写 config.toml：未被识别的注释会丢失，语义不变。"
