package registry

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"pondera/internal/config"
)

// reflectPathSet is an independent walker used only by the audit. It deliberately
// does not share code with registry.go: the registry must describe every TOML
// field that the real Config struct exposes, or the test goes red on drift.
func reflectPathSet(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var walk func(t reflect.Type, prefix string, stack map[reflect.Type]bool)
	walk = func(rt reflect.Type, prefix string, stack map[reflect.Type]bool) {
		for rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct {
			return
		}
		if stack[rt] {
			return
		}
		stack[rt] = true
		defer delete(stack, rt)
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if sf.PkgPath != "" {
				continue
			}
			parts := strings.Split(sf.Tag.Get("toml"), ",")
			name := parts[0]
			if name == "" || name == "-" {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			ft := sf.Type
			switch {
			case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
				walk(ft.Elem(), path, stack)
			case ft.Kind() == reflect.Struct:
				walk(ft, path, stack)
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
				walk(ft.Elem(), path+"[]", stack)
			default:
				seen[path] = true
			}
		}
	}
	walk(reflect.TypeFor[config.Config](), "", map[reflect.Type]bool{})
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// TestEveryTOMLFieldIsRegistered fails whenever the Config struct gains or
// loses a TOML field without the registry being updated.
func TestEveryTOMLFieldIsRegistered(t *testing.T) {
	r := Default()
	expected := reflectPathSet(t)
	actual := r.Paths()
	if strings.Join(expected, "\n") != strings.Join(actual, "\n") {
		missing := diff(expected, actual)
		extra := diff(actual, expected)
		t.Fatalf("registry drift: missing from registry %v; registry-only %v", missing, extra)
	}
}

func diff(a, b []string) []string {
	set := map[string]bool{}
	for _, v := range b {
		set[v] = true
	}
	var out []string
	for _, v := range a {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}

// TestCuratedFieldsExist guards against a typo in a hand-authored path.
func TestCuratedFieldsExist(t *testing.T) {
	r := Default()
	for path := range curatedFields {
		if _, ok := r.Lookup(path); !ok {
			t.Errorf("curated path not present in reflected schema: %s", path)
		}
	}
}

// TestDeniedGroupsAreNeverWritable keeps the security boundary closed even if
// a future curated entry is added by mistake.
func TestDeniedGroupsAreNeverWritable(t *testing.T) {
	r := Default()
	for _, f := range r.All() {
		if IsDeniedGroup(f.Group) && f.Writable {
			t.Errorf("%s: denied group must not be writable", f.Path)
		}
	}
}

// TestOnlyThreeWriteSurfaces pins the MVP write surface: default_model, curated
// agent knobs and five provider-scoped fields.
func TestOnlyThreeWriteSurfaces(t *testing.T) {
	r := Default()
	want := map[string]bool{
		"default_model":                 true,
		"language":                      true,
		"agent.temperature":             true,
		"agent.compact_ratio":           true,
		"agent.retry_count":             true,
		"agent.planner_model":           true,
		"agent.subagent_model":          true,
		"agent.vision_model":            true,
		"agent.web_search_model":        true,
		"providers[].model":             true,
		"providers[].default":           true,
		"providers[].context_window":    true,
		"providers[].max_output_tokens": true,
		"providers[].effort":            true,
	}
	got := r.Writable()
	if len(got) != len(want) {
		t.Fatalf("writable surface changed: got %d entries, want %d", len(got), len(want))
	}
	for _, f := range got {
		if !want[f.Path] {
			t.Errorf("unexpected writable path %s", f.Path)
		}
	}
}
