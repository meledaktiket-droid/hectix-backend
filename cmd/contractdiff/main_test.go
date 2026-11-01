package main

import (
	"strings"
	"testing"
)

func mustDecode(t *testing.T, s string) any {
	t.Helper()
	v, ok := decode([]byte(s))
	if !ok {
		t.Fatalf("bad json %s", s)
	}
	return v
}

func TestFirstDiff(t *testing.T) {
	cases := []struct {
		a, b, path, x, y string
	}{
		{`{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1}`, "", "", ""},
		{`{"a":1}`, `{"a":"1"}`, "$.a", "1", `"1"`},
		{`{"a":{"c":1,"d":2}}`, `{"a":{"c":1,"d":3}}`, "$.a.d", "2", "3"},
		{`{"a":1}`, `{"a":1,"b":null}`, "$.b", "<missing>", "null"},
		{`{"a":1,"b":2}`, `{"a":1}`, "$.b", "2", "<missing>"},
		{`[1,2,3]`, `[1,2]`, "$.length", "3", "2"},
		{`[{"id":"x"},{"id":"y"}]`, `[{"id":"y"},{"id":"x"}]`, "$[0].id", `"x"`, `"y"`},
		{`{"t":"2026-01-01T00:00:00.000Z"}`, `{"t":"2026-01-01T00:00:00Z"}`, "$.t", `"2026-01-01T00:00:00.000Z"`, `"2026-01-01T00:00:00Z"`},
		{`{"a":[]}`, `{"a":null}`, "$.a", "[]", "null"},
		{`{"a":1.0}`, `{"a":1}`, "", "", ""},
	}
	for _, c := range cases {
		p, x, y, d := firstDiff("$", mustDecode(t, c.a), mustDecode(t, c.b))
		if (c.path != "") != d || p != c.path {
			t.Errorf("%s vs %s: got path %q diff=%v, want %q", c.a, c.b, p, d, c.path)
			continue
		}
		if d && (show(x) != c.x || show(y) != c.y) {
			t.Errorf("%s vs %s: got %s / %s, want %s / %s", c.a, c.b, show(x), show(y), c.x, c.y)
		}
	}
}

func TestCompare(t *testing.T) {
	j := func(s string) response {
		return response{status: 200, contentType: "application/json", body: []byte(s)}
	}
	if m := compare("json", j(`{"a":1}`), j(`{ "a" : 1 }`)); m != "" {
		t.Errorf("equal json: %s", m)
	}
	if m := compare("json", response{status: 401}, response{status: 200}); !strings.Contains(m, "status old 401 new 200") {
		t.Errorf("status: %s", m)
	}
	pdf := func(n int) response {
		return response{status: 200, contentType: "application/pdf", body: make([]byte, n)}
	}
	if m := compare("bin", pdf(10), pdf(20)); m != "" {
		t.Errorf("bin ignores body: %s", m)
	}
	if m := compare("json", pdf(10), pdf(20)); !strings.Contains(m, "non-JSON") {
		t.Errorf("non-json length: %s", m)
	}
	csv := func(s string) response { return response{status: 200, contentType: "text/csv", body: []byte(s)} }
	if m := compare("bytes", csv("a\nb\nc"), csv("a\nB\nc")); !strings.HasPrefix(m, "line 2") {
		t.Errorf("bytes: %s", m)
	}
}

func TestParseContracts(t *testing.T) {
	cs, err := parseContracts(strings.NewReader("# c\n\nweb /api/events\n~admin /api/x bin\n"))
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	if cs[0] != (contract{app: "web", path: "/api/events", mode: "json"}) || cs[1] != (contract{"admin", "/api/x", "bin", true}) {
		t.Fatalf("%+v", cs)
	}
	if _, err := parseContracts(strings.NewReader("web /a nope\n")); err == nil {
		t.Fatal("want mode error")
	}
}

func TestDiffsAll(t *testing.T) {
	a := mustDecode(t, `{"a":1,"b":[1,2,3],"c":{"d":true}}`)
	b := mustDecode(t, `{"a":2,"b":[1,5],"c":{"d":true,"e":1}}`)
	var got []string
	for _, d := range diffs("$", a, b, 0) {
		got = append(got, d.path+"="+show(d.old)+"/"+show(d.new))
	}
	want := "$.a=1/2 $.b[1]=2/5 $.b.length=3/2 $.c.e=<missing>/1"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v want %s", got, want)
	}
	if n := len(diffs("$", a, b, 2)); n != 2 {
		t.Fatalf("limit: got %d", n)
	}
}
