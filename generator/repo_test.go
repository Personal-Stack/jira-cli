package generator

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCommittedOutputUpToDate fails when generated/ is stale relative to
// specs/ (the same check as `make verify-generated`).
func TestCommittedOutputUpToDate(t *testing.T) {
	res, err := Generate(Options{Root: "..", Manifest: "specs/manifest.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range res.Files {
		got, err := os.ReadFile(filepath.Join("..", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is out of date; run `make generate`", name)
		}
	}
}
