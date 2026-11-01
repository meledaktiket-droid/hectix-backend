package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type contract struct {
	app, path, mode string
	expectDiff      bool
}

func parseContracts(r io.Reader) ([]contract, error) {
	var out []contract
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c := contract{mode: "json"}
		if rest, ok := strings.CutPrefix(line, "~"); ok {
			c.expectDiff, line = true, strings.TrimSpace(rest)
		}
		f := strings.Fields(line)
		if len(f) < 2 || len(f) > 3 {
			return nil, fmt.Errorf("line %d: want `<app> <path> [mode]`", n)
		}
		c.app, c.path = f[0], f[1]
		if len(f) == 3 {
			c.mode = f[2]
		}
		if !slices.Contains([]string{"json", "bin", "bytes"}, c.mode) {
			return nil, fmt.Errorf("line %d: unknown mode %q", n, c.mode)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

type response struct {
	status      int
	contentType string
	disposition string
	body        []byte
}

var client = &http.Client{
	Timeout:       60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func fetch(base, host, path, cookie string) (response, error) {
	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		return response{}, err
	}
	if host != "" {
		req.Host = host
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return response{res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"), b}, err
}

type missing struct{}

func (missing) String() string { return "<missing>" }

type diff struct {
	path     string
	old, new any
}

func diffs(path string, a, b any, limit int) []diff {
	var out []diff
	var walk func(path string, a, b any) bool
	add := func(d diff) bool {
		out = append(out, d)
		return limit <= 0 || len(out) < limit
	}
	walk = func(path string, a, b any) bool {
		switch av := a.(type) {
		case map[string]any:
			bv, ok := b.(map[string]any)
			if !ok {
				return add(diff{path, a, b})
			}
			keys := make([]string, 0, len(av)+len(bv))
			for k := range av {
				keys = append(keys, k)
			}
			for k := range bv {
				if _, ok := av[k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				x, xok := av[k]
				y, yok := bv[k]
				p := path + "." + k
				switch {
				case !xok:
					if !add(diff{p, missing{}, y}) {
						return false
					}
				case !yok:
					if !add(diff{p, x, missing{}}) {
						return false
					}
				default:
					if !walk(p, x, y) {
						return false
					}
				}
			}
			return true
		case []any:
			bv, ok := b.([]any)
			if !ok {
				return add(diff{path, a, b})
			}
			for i := range min(len(av), len(bv)) {
				if !walk(path+"["+strconv.Itoa(i)+"]", av[i], bv[i]) {
					return false
				}
			}
			if len(av) != len(bv) {
				return add(diff{path + ".length", len(av), len(bv)})
			}
			return true
		default:
			if a != b {
				return add(diff{path, a, b})
			}
			return true
		}
	}
	walk(path, a, b)
	return out
}

func firstDiff(path string, a, b any) (string, any, any, bool) {
	if d := diffs(path, a, b, 1); len(d) > 0 {
		return d[0].path, d[0].old, d[0].new, true
	}
	return "", nil, nil, false
}

func decode(b []byte) (any, bool) {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil, false
	}
	return v, true
}

func show(v any) string {
	if m, ok := v.(missing); ok {
		return m.String()
	}
	b, _ := json.Marshal(v)
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

func compare(mode string, o, n response) string {
	if o.status != n.status {
		return fmt.Sprintf("status old %d new %d", o.status, n.status)
	}
	switch mode {
	case "bin":
		if o.contentType != n.contentType {
			return fmt.Sprintf("content-type old %q new %q", o.contentType, n.contentType)
		}
		if o.disposition != n.disposition {
			return fmt.Sprintf("content-disposition old %q new %q", o.disposition, n.disposition)
		}
		return ""
	case "bytes":
		if bytes.Equal(o.body, n.body) {
			return ""
		}
		ol, nl := strings.Split(string(o.body), "\n"), strings.Split(string(n.body), "\n")
		for i := range min(len(ol), len(nl)) {
			if ol[i] != nl[i] {
				return fmt.Sprintf("line %d\n    old: %.300s\n    new: %.300s", i+1, ol[i], nl[i])
			}
		}
		return fmt.Sprintf("line count old %d new %d", len(ol), len(nl))
	}
	ov, ook := decode(o.body)
	nv, nok := decode(n.body)
	if !ook || !nok {
		if ook != nok || o.contentType != n.contentType || len(o.body) != len(n.body) {
			return fmt.Sprintf("non-JSON body: old %q %dB, new %q %dB", o.contentType, len(o.body), n.contentType, len(n.body))
		}
		return ""
	}
	if p, x, y, d := firstDiff("$", ov, nv); d {
		return fmt.Sprintf("%s\n    old: %s\n    new: %s", p, show(x), show(y))
	}
	return ""
}

func envFor(prefix, app, suffix string) string {
	up := strings.ToUpper(app)
	if v := os.Getenv(prefix + up + suffix); v != "" {
		return v
	}
	return os.Getenv(prefix + strings.TrimRight(up, "0123456789") + suffix)
}

func dump(label string, r response) {
	fmt.Printf("--- %s: %d %s\n", label, r.status, r.contentType)
	if v, ok := decode(r.body); ok {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
	} else if r.contentType == "" || strings.HasPrefix(r.contentType, "text/") {
		fmt.Println(string(r.body))
	} else {
		fmt.Printf("<%d bytes>\n", len(r.body))
	}
}

func main() {
	verbose := flag.String("v", "", "dump both full bodies for this contract (`app /path` or `/path`)")
	run := flag.String("run", "", "only check contracts whose `app path` contains this substring")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: contractdiff [-v 'app /path'] [-run substr] contracts.txt")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cs, err := parseContracts(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var failed, expected, checked int
	for _, c := range cs {
		key := c.app + " " + c.path
		if *run != "" && !strings.Contains(key, *run) {
			continue
		}
		checked++
		cookie := os.Getenv(strings.ToUpper(c.app) + "_COOKIE")
		o, err1 := fetch(envFor("OLD_", c.app, ""), "", c.path, cookie)
		n, err2 := fetch(os.Getenv("NEW"), envFor("", c.app, "_HOST"), c.path, cookie)
		if *verbose == key || *verbose == c.path {
			dump("old "+key, o)
			dump("new "+key, n)
			ov, ook := decode(o.body)
			nv, nok := decode(n.body)
			if ook && nok {
				fmt.Println("--- all differences:")
				for _, d := range diffs("$", ov, nv, 0) {
					fmt.Printf("  %s: old %s new %s\n", d.path, show(d.old), show(d.new))
				}
			}
		}
		var msg string
		switch {
		case err1 != nil:
			msg = "old: " + err1.Error()
		case err2 != nil:
			msg = "new: " + err2.Error()
		default:
			msg = compare(c.mode, o, n)
		}
		switch {
		case msg == "" && c.expectDiff:
			fmt.Printf("ok   %s (marked ~ but now matches)\n", key)
		case msg == "":
			fmt.Printf("ok   %s\n", key)
		case c.expectDiff:
			expected++
			fmt.Printf("xdif %s: %s\n", key, msg)
		default:
			failed++
			fmt.Printf("DIFF %s: %s\n", key, msg)
		}
	}
	fmt.Printf("%d checked, %d differ, %d expected differences\n", checked, failed, expected)
	if failed > 0 {
		os.Exit(1)
	}
}
