package eval_runner

import "testing"

func TestRenderPromptTemplate(t *testing.T) {
	tmpl := []interface{}{
		map[string]interface{}{"role": "system", "content": "Be terse."},
		map[string]interface{}{"role": "user", "content": "Echo {{input}} (want {{ expected }})"},
	}
	out := renderPromptTemplate(tmpl, map[string]interface{}{"input": "green"}, "GREEN", "mustache")
	if len(out) != 2 {
		t.Fatalf("want 2 messages, got %d", len(out))
	}
	if out[1]["content"] != "Echo green (want GREEN)" {
		t.Fatalf("bad substitution: %q", out[1]["content"])
	}
	// engine "none" leaves vars untouched
	none := renderPromptTemplate(tmpl, "x", "y", "none")
	if none[1]["content"] != "Echo {{input}} (want {{ expected }})" {
		t.Fatalf("none engine should not substitute: %q", none[1]["content"])
	}
}

func TestTemplateText(t *testing.T) {
	cases := []struct{ in interface{}; want string }{
		{"hi", "hi"},
		{map[string]interface{}{"input": "green"}, "green"},
		{map[string]interface{}{"only": "v"}, "v"},
		{float64(3), "3"},
		{true, "true"},
	}
	for _, c := range cases {
		if got := templateText(c.in); got != c.want {
			t.Errorf("templateText(%v)=%q want %q", c.in, got, c.want)
		}
	}
}
