// Package registry describes every field Pondera can write from the config
// assistant. The registry is the single source of truth for paths, types,
// bounds and risk; the assistant never invents a path the registry does not
// describe.
package registry

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"pondera/internal/config"
)

// Type is a scalar wire type the assistant may write.
type Type string

const (
	TypeString Type = "string"
	TypeInt    Type = "int"
	TypeFloat  Type = "float"
	TypeBool   Type = "bool"
	// TypeComplex marks arrays, tables, maps and other non-scalar values. They
	// are described for auditing but can never be written as one scalar patch.
	TypeComplex Type = "complex"
)

// Risk decides how an accepted patch is surfaced. deny is absolute: the local
// executor refuses it before any file is touched.
type Risk string

const (
	RiskBenign  Risk = "benign"
	RiskConfirm Risk = "confirm"
	RiskDeny    Risk = "deny"
)

// DeniedGroups are configuration groups that can change the security boundary
// or execute code. They stay readable (redacted) and are never writable.
var DeniedGroups = map[string]bool{
	"serve":       true,
	"bot":         true,
	"network":     true,
	"sandbox":     true,
	"permissions": true,
	"secrets":     true,
	"plugins":     true,
	"statusline":  true,
}

// Field is one registry entry.
type Field struct {
	Path      string   // e.g. "agent.temperature" or "providers[].model"
	Group     string   // first path segment
	Type      Type     // scalar wire type
	Risk      Risk     // benign | confirm | deny
	Writable  bool     // may appear in a patch op
	Sensitive bool     // value must never reach logs/UI/model context
	Doc       string   // human-readable meaning
	Enum      []string // allowed strings, empty = unrestricted
	Min       *float64 // inclusive lower bound for numbers
	Max       *float64 // inclusive upper bound for numbers
}

// IsDeniedGroup reports whether group is one of the security-boundary groups.
func IsDeniedGroup(group string) bool { return DeniedGroups[group] }

// Registry is an immutable field map plus the reflection audit surface.
type Registry struct {
	fields map[string]Field
}

var defaultRegistry = build()

// Default returns the process-wide registry.
func Default() *Registry { return defaultRegistry }

// Lookup returns one field by path.
func (r *Registry) Lookup(path string) (Field, bool) {
	f, ok := r.fields[path]
	return f, ok
}

// All returns every entry sorted by path.
func (r *Registry) All() []Field {
	out := make([]Field, 0, len(r.fields))
	for _, f := range r.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Writable returns the fields a patch op may target.
func (r *Registry) Writable() []Field {
	out := make([]Field, 0)
	for _, f := range r.All() {
		if f.Writable {
			out = append(out, f)
		}
	}
	return out
}

// Paths returns every registered path, used by the struct-drift audit.
func (r *Registry) Paths() []string {
	out := make([]string, 0, len(r.fields))
	for path := range r.fields {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func build() *Registry {
	r := &Registry{fields: map[string]Field{}}
	walkStruct(reflect.TypeFor[config.Config](), "", nil, r.fields)
	for path, f := range r.fields {
		if curated, ok := curatedFields[path]; ok {
			curated.Path = path
			curated.Group = f.Group
			if curated.Type == "" {
				curated.Type = f.Type
			}
			r.fields[path] = curated
			continue
		}
		if IsDeniedGroup(f.Group) {
			f.Risk = RiskDeny
		}
		r.fields[path] = f
	}
	return r
}

// walkStruct records every exported TOML field of t. Nested tables and slices
// of tables recurse with a path segment; maps and scalars stop the walk. This
// describes structure only — risk and writability come from curatedFields.
func walkStruct(t reflect.Type, prefix string, stack map[reflect.Type]bool, out map[string]Field) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	if stack == nil {
		stack = map[reflect.Type]bool{}
	}
	if stack[t] {
		return
	}
	stack[t] = true
	defer delete(stack, t)

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		tag := sf.Tag.Get("toml")
		if tag == "-" || tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		ft := sf.Type
		switch {
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
			walkStruct(ft.Elem(), path, stack, out)
		case ft.Kind() == reflect.Struct:
			walkStruct(ft, path, stack, out)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			walkStruct(ft.Elem(), path+"[]", stack, out)
		default:
			out[path] = Field{
				Path:  path,
				Group: strings.Split(path, ".")[0],
				Type:  wireType(ft),
				Risk:  RiskDeny,
			}
		}
	}
}

func wireType(t reflect.Type) Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return TypeString
	case reflect.Bool:
		return TypeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return TypeInt
	case reflect.Float32, reflect.Float64:
		return TypeFloat
	}
	return TypeComplex
}

func floatPtr(v float64) *float64 { return &v }

// curatedFields is the hand-authored policy on top of the reflected structure.
// Only these paths can be written in the MVP.
var curatedFields = map[string]Field{
	"default_model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "默认模型引用（provider/model）。只影响新会话。",
	},
	"language": {
		Type: TypeString, Risk: RiskBenign, Writable: true,
		Doc: "界面与模型语言标签，空值表示自动检测。",
	},

	"agent.temperature": {
		Type: TypeFloat, Risk: RiskBenign, Writable: true,
		Doc: "默认采样温度。0–2，越高越发散。", Min: floatPtr(0), Max: floatPtr(2),
	},
	"agent.compact_ratio": {
		Type: TypeFloat, Risk: RiskConfirm, Writable: true,
		Doc: "上下文压缩触发比例。0.5–0.95，越早压缩越省 token 但丢细节。", Min: floatPtr(0.5), Max: floatPtr(0.95),
	},
	"agent.retry_count": {
		Type: TypeInt, Risk: RiskBenign, Writable: true,
		Doc: "模型请求失败后的重试次数。0–1000，0 表示不重试。", Min: floatPtr(0), Max: floatPtr(1000),
	},
	"agent.planner_model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "双模型协同时的规划模型引用；空值表示关闭。",
	},
	"agent.subagent_model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "子代理默认模型引用；空值表示继承主模型。",
	},
	"agent.vision_model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "图片预处理模型引用；空值表示关闭，auto 表示自动选择。",
	},
	"agent.web_search_model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "独立联网搜索使用的模型引用；空值或 auto 表示自动选择。",
	},

	"providers[].model": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "该 provider 的单个模型名（兼容字段）。",
	},
	"providers[].default": {
		Type: TypeString, Risk: RiskConfirm, Writable: true,
		Doc: "多模型 provider 的默认模型名。",
	},
	"providers[].context_window": {
		Type: TypeInt, Risk: RiskConfirm, Writable: true,
		Doc: "该模型上下文窗口 token 数；0 表示关闭压缩。", Min: floatPtr(1000),
	},
	"providers[].max_output_tokens": {
		Type: TypeInt, Risk: RiskBenign, Writable: true,
		Doc: "单轮输出 token 上限，0 表示使用厂商默认。", Min: floatPtr(0),
	},
	"providers[].effort": {
		Type: TypeString, Risk: RiskBenign, Writable: true,
		Doc:  "推理强度。",
		Enum: []string{"auto", "disabled", "low", "medium", "high", "xhigh", "max"},
	},
}

// ProviderWritableFields lists the provider-scoped fields accepted by
// set_provider_field. Order is stable for prompts and tests.
var ProviderWritableFields = []string{"model", "default", "context_window", "max_output_tokens", "effort"}

// IsProviderField reports whether field belongs to the provider write allow-list.
func IsProviderField(field string) bool {
	for _, candidate := range ProviderWritableFields {
		if candidate == field {
			return true
		}
	}
	return false
}

func (r *Registry) providerField(field string) (Field, bool) {
	f, ok := r.fields["providers[]."+field]
	return f, ok
}

// Lookup returns a provider-scoped field descriptor.
func (r *Registry) ProviderField(field string) (Field, bool) { return r.providerField(field) }

// CheckValue validates a typed value against a field descriptor.
func CheckValue(f Field, value any) error {
	switch f.Type {
	case TypeString:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("type_mismatch: %s 需要字符串", f.Path)
		}
		if len(f.Enum) > 0 {
			allowed := false
			for _, e := range f.Enum {
				if e == s {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("enum_mismatch: %s 只接受 %s", f.Path, strings.Join(f.Enum, "|"))
			}
		}
	case TypeInt:
		n, ok := value.(int64)
		if !ok {
			return fmt.Errorf("type_mismatch: %s 需要整数", f.Path)
		}
		if err := checkBounds(f, float64(n)); err != nil {
			return err
		}
	case TypeFloat:
		n, ok := value.(float64)
		if !ok {
			return fmt.Errorf("type_mismatch: %s 需要数字", f.Path)
		}
		if err := checkBounds(f, n); err != nil {
			return err
		}
	case TypeBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("type_mismatch: %s 需要布尔值", f.Path)
		}
	default:
		return fmt.Errorf("type_mismatch: %s 不是可写标量", f.Path)
	}
	return nil
}

func checkBounds(f Field, n float64) error {
	if f.Min != nil && n < *f.Min {
		return fmt.Errorf("out_of_range: %s 最小值 %s，收到 %v", f.Path, trimFloat(*f.Min), n)
	}
	if f.Max != nil && n > *f.Max {
		return fmt.Errorf("out_of_range: %s 最大值 %s，收到 %v", f.Path, trimFloat(*f.Max), n)
	}
	return nil
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}
