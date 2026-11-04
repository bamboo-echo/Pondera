package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"pondera/internal/assist/registry"
	"pondera/internal/config"
)

var credentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{16,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
}

// Redact replaces any known credential value and common key shapes with a
// marker. It runs before chat text ever reaches the model.
func Redact(text string, cfg *config.Config) string {
	if cfg != nil {
		for i := range cfg.Providers {
			key := strings.TrimSpace(cfg.Providers[i].APIKeyEnv)
			if key == "" {
				continue
			}
			if value := strings.TrimSpace(os.Getenv(key)); len(value) >= 8 && strings.Contains(text, value) {
				text = strings.ReplaceAll(text, value, "[已脱敏]")
			}
		}
	}
	for _, shape := range credentialShapes {
		text = shape.ReplaceAllString(text, "[已脱敏]")
	}
	return text
}

// SystemPrompt is the contract the model must follow. It lists the only
// writable paths and restates the hard rejects.
func SystemPrompt() string {
	var b strings.Builder
	b.WriteString("你是 Pondera 的配置助手。你只能输出一个 JSON 信封，不能输出任何解释性正文以外的内容。\n\n")
	b.WriteString("信封格式（严格，字段不能增减）：\n")
	b.WriteString(`{"version":1,"intent":"一句话意图，<=200字","reply":"给用户看的简短说明","ops":[...]}`)
	b.WriteString("\n\n可用的 op（最多 10 个）：\n")
	b.WriteString(`- {"op":"set","path":"<白名单路径>","value":<标量>}` + "\n")
	b.WriteString(`- {"op":"set_provider_field","provider":"<provider 名称>","field":"<白名单字段>","value":<标量>}` + "\n")
	b.WriteString(`- {"op":"set_secret","api_key_env":"<被 provider 引用的变量名>","value":"<密钥>"}` + "\n\n")
	b.WriteString("可写路径与取值约束：\n")
	for _, f := range registry.Default().Writable() {
		if strings.HasPrefix(f.Path, "providers[].") {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s) %s", f.Path, f.Type, f.Doc)
		if len(f.Enum) > 0 {
			fmt.Fprintf(&b, " 取值：%s", strings.Join(f.Enum, "|"))
		}
		if f.Min != nil || f.Max != nil {
			fmt.Fprintf(&b, " 范围：%v..%v", ptr(f.Min), ptr(f.Max))
		}
		b.WriteString("\n")
	}
	b.WriteString("- providers[].<field> 可写字段：")
	b.WriteString(strings.Join(registry.ProviderWritableFields, ", "))
	b.WriteString("\n\n")
	b.WriteString("硬规则：\n")
	b.WriteString("1. 绝不输出整份 TOML/JSON、文件路径或 shell 命令；value 必须是字符串/数字/布尔值。\n")
	b.WriteString("2. 绝不写 serve/bot/network/sandbox/permissions/secrets/plugins/statusline 组。\n")
	b.WriteString("3. 绝不回显任何密钥原文；需要设置密钥时用 set_secret，值来自用户明确给出。\n")
	b.WriteString("4. default_model 与模型引用必须能在给定配置快照里解析。\n")
	b.WriteString("5. 只解释、不修改时，ops 填空数组 [] 并在 reply 里回答；不要输出信封以外的内容。\n")
	return b.String()
}

func ptr(v *float64) any {
	if v == nil {
		return "无"
	}
	return *v
}

// UserPrompt renders the redacted snapshot plus the user's request.
func UserPrompt(snap registry.Snapshot, userMessage string) string {
	raw, _ := json.MarshalIndent(snap, "", "  ")
	return "当前配置快照（已脱敏，不含密钥）：\n" + string(raw) + "\n\n用户请求：\n" + userMessage
}
