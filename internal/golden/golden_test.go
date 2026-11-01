package golden

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameShape(t *testing.T) {
	a := []byte(`{"user":{"id":"1","image":null},"list":[{"x":1}]}`)
	b := []byte(`{"user":{"id":"2","image":"u"},"list":[{"x":5}]}`)
	if d := Diff(a, b); d != "" {
		t.Fatalf("want same shape, got %s", d)
	}
	c := []byte(`{"user":{"id":"2"},"list":[]}`)
	if Diff(a, c) == "" {
		t.Fatal("want diff for missing key")
	}
}

func TestGoldenFilesParse(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "golden")
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			t.Errorf("invalid JSON: %s", path)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no golden files found")
	}
}
