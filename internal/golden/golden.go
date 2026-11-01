package golden

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

func Diff(want, got []byte) string {
	var a, b any
	if err := json.Unmarshal(want, &a); err != nil {
		return "want: " + err.Error()
	}
	if err := json.Unmarshal(got, &b); err != nil {
		return "got: " + err.Error()
	}
	var out []string
	walk("$", a, b, &out)
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func walk(path string, a, b any, out *[]string) {
	if a == nil || b == nil {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, path+": want object")
			return
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok {
				*out = append(*out, path+"."+k+": missing")
				continue
			}
			walk(path+"."+k, v, w, out)
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			*out = append(*out, path+": want array")
			return
		}
		if len(av) > 0 && len(bv) > 0 {
			walk(path+"[0]", av[0], bv[0], out)
		} else if len(av) > 0 && len(bv) == 0 {
			*out = append(*out, path+": empty, golden has items")
		}
	default:
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", b) {
			*out = append(*out, fmt.Sprintf("%s: want %T got %T", path, a, b))
		}
	}
}

func Assert(t *testing.T, file string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(want, got); d != "" {
		t.Fatalf("shape mismatch vs %s:\n%s\ngot: %s", file, d, got)
	}
}
