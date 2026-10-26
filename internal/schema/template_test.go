package schema

import "testing"

func render(t *testing.T, tmpl string, vals map[string]string) string {
	t.Helper()
	ty := &Type{DisplayTemplate: tmpl}
	return ty.RenderDisplay(func(k string) string { return vals[k] })
}

func TestRenderDisplay(t *testing.T) {
	cases := []struct {
		name, tmpl string
		vals       map[string]string
		want       string
	}{
		{"cpu", "{name} · {model|cpu}", map[string]string{"name": "Proc 1", "model": "Intel(R) Xeon(R) CPU E5-2695 v4 @ 2.10GHz"}, "Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz"},
		{"cpu word in the middle", "{model|cpu}", map[string]string{"model": "Intel(R) Xeon(R) Gold 6138 CPU @ 2.00GHz"}, "Intel Xeon Gold 6138 @ 2.00GHz"},
		{"missing attribute drops its separator", "{name} · {model|cpu}", map[string]string{"name": "Proc 2"}, "Proc 2"},
		{"missing first falls back, whatever the rest says", "{name} · {model}", map[string]string{"model": "X1"}, ""},
		{"empty everywhere", "{name} · {model}", map[string]string{}, ""},
		{"prefix and suffix", "[{name}] {model} !", map[string]string{"name": "a", "model": "b"}, "[a] b !"},
		{"prefix is not rendered without the first", "[{name}] {model}", map[string]string{"model": "b"}, ""},
		{"dimm", "{name} · {technology} {size_bytes|ram}", map[string]string{"name": "DIMM A1", "technology": "DDR4", "size_bytes": "17179869184"}, "DIMM A1 · DDR4 16 GB"},
		{"dimm without technology", "{name} · {technology} {size_bytes|ram}", map[string]string{"name": "PROC 1 DIMM 12", "size_bytes": "68719476736"}, "PROC 1 DIMM 12 · 64 GB"},
		{"disk", "{name} · {media|media} {capacity_bytes|bytes} {model}", map[string]string{"name": "1I:0:1", "media": "ssd", "capacity_bytes": "500107837440", "model": "CT500MX500SSD1"}, "1I:0:1 · SSD 500 GB CT500MX500SSD1"},
		{"disk unknown media", "{name} · {media|media} {capacity_bytes|bytes}", map[string]string{"name": "sda", "media": "unknown", "capacity_bytes": "1920383410176"}, "sda · 1.9 TB"},
		{"psu", "{name} · {capacity_w|watts} {model}", map[string]string{"name": "HpeServerPowerSupply 0", "capacity_w": "800", "model": "865414-B21"}, "HpeServerPowerSupply 0 · 800 W 865414-B21"},
		{"not a number", "{name} · {capacity_w|watts}", map[string]string{"name": "p", "capacity_w": "lots"}, "p"},
		{"1.5e10 float form", "{x|bytes}", map[string]string{"x": "1.7179869184e+10"}, "17.2 GB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render(t, c.tmpl, c.vals); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	if got := (&Type{}).RenderDisplay(func(string) string { return "x" }); got != "" {
		t.Errorf("a type without template renders %q", got)
	}
}

func TestBuiltinTemplatesAreValid(t *testing.T) {
	r := Builtin()
	for _, n := range []string{"cpu", "memory", "disk", "psu"} {
		if r.Types[n].DisplayTemplate == "" {
			t.Errorf("type %s has no display template", n)
		}
	}
	if _, ok := r.Types["memory"].Attributes["technology"]; !ok {
		t.Error("memory lacks technology")
	}
}
