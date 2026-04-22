package keystone

import "testing"

func TestRenderSimpleVar(t *testing.T) {
	out := RenderTemplate("Hello {name}", map[string]interface{}{"name": "alex"})
	if out != "Hello alex" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderDoubleBrace(t *testing.T) {
	out := RenderTemplate("Hi {{who}}!", map[string]interface{}{"who": "world"})
	if out != "Hi world!" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderList(t *testing.T) {
	tmpl := "Hobbies:\n{#hobbies}- {_it}\n{/hobbies}"
	vars := map[string]interface{}{"hobbies": []interface{}{"climbing", "typing"}}
	expected := "Hobbies:\n- climbing\n- typing\n"
	if got := RenderTemplate(tmpl, vars); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestRenderListOfObjects(t *testing.T) {
	tmpl := "{#items}[{name}={value}]{/items}"
	vars := map[string]interface{}{"items": []interface{}{
		map[string]interface{}{"name": "x", "value": 1},
		map[string]interface{}{"name": "y", "value": 2},
	}}
	if out := RenderTemplate(tmpl, vars); out != "[x=1][y=2]" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderDotPath(t *testing.T) {
	out := RenderTemplate("Hello {user.name}", map[string]interface{}{
		"user": map[string]interface{}{"name": "alex"},
	})
	if out != "Hello alex" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderInterleavedSections(t *testing.T) {
	tmpl := "{#a}A:{_it}{/a}|{#b}B:{_it}{/b}"
	vars := map[string]interface{}{
		"a": []interface{}{1, 2},
		"b": []interface{}{3},
	}
	want := "A:1A:2|B:3"
	if got := RenderTemplate(tmpl, vars); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderFalsySection(t *testing.T) {
	tmpl := "start{#hidden}INNER{/hidden}end"
	if got := RenderTemplate(tmpl, map[string]interface{}{"hidden": false}); got != "startend" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptRenderOverrides(t *testing.T) {
	p := Prompt{
		Slug:      "greet",
		Template:  "Hi {who}",
		Variables: map[string]interface{}{"who": "world"},
	}
	if got := p.Render(nil); got != "Hi world" {
		t.Fatalf("default render got %q", got)
	}
	if got := p.Render(map[string]interface{}{"who": "alex"}); got != "Hi alex" {
		t.Fatalf("override render got %q", got)
	}
}
