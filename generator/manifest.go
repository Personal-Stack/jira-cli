// Package generator turns OpenAPI specifications into compiled-in CLI
// operation tables. It runs at development/build time only (cmd/jiragen);
// the runtime CLI never imports it.
package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Manifest is specs/manifest.yaml: the list of profiles to generate and the
// provenance (version + checksum) of every input file.
type Manifest struct {
	Version  int           `yaml:"version"`
	Profiles []ProfileSpec `yaml:"profiles"`
}

// ProfileSpec configures one generated profile.
type ProfileSpec struct {
	Name       string    `yaml:"name"`
	Deployment string    `yaml:"deployment"`
	BasePath   string    `yaml:"base_path"`
	Specs      []SpecRef `yaml:"specs"`    // first is the base; later ones add operations
	Overlays   []FileRef `yaml:"overlays"` // naming/parameter fixes and exclusions, applied in order
	Risk       *FileRef  `yaml:"risk"`
}

// SpecRef points at an OpenAPI document and pins its checksum.
type SpecRef struct {
	Name    string `yaml:"name"`
	Source  string `yaml:"source"` // official, custom, remote
	Path    string `yaml:"path"`
	URL     string `yaml:"url,omitempty"` // remote only; used by `jiragen update`
	Version string `yaml:"version"`       // must equal info.version of the file
	SHA256  string `yaml:"sha256"`
}

// FileRef points at a YAML metadata file and pins its checksum.
type FileRef struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

var profileNameRe = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,62}$`)

// LoadManifest reads and structurally validates a manifest.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest %s: unsupported version %d", path, m.Version)
	}
	if len(m.Profiles) == 0 {
		return nil, fmt.Errorf("manifest %s: no profiles", path)
	}
	seen := map[string]bool{}
	for _, p := range m.Profiles {
		if !profileNameRe.MatchString(p.Name) {
			return nil, fmt.Errorf("manifest: invalid profile name %q", p.Name)
		}
		if seen[p.Name] || seen[PackageName(p.Name)] {
			return nil, fmt.Errorf("manifest: duplicate profile %q", p.Name)
		}
		seen[p.Name], seen[PackageName(p.Name)] = true, true
		if p.Deployment != "datacenter" {
			return nil, fmt.Errorf("profile %s: unsupported deployment %q (only datacenter)", p.Name, p.Deployment)
		}
		if len(p.Specs) == 0 {
			return nil, fmt.Errorf("profile %s: no specs", p.Name)
		}
		for _, s := range p.Specs {
			switch s.Source {
			case "official", "custom", "remote":
			default:
				return nil, fmt.Errorf("profile %s: spec %s: unsupported source %q", p.Name, s.Name, s.Source)
			}
			if s.Name == "" || s.Path == "" {
				return nil, fmt.Errorf("profile %s: spec requires name and path", p.Name)
			}
		}
	}
	return &m, nil
}

// Save writes the manifest back (used by `jiragen update`).
func (m *Manifest) Save(path string) error {
	var buf bytes.Buffer
	buf.WriteString("# Specification manifest. Versions and checksums are verified by `make generate`;\n# refresh them with `go run ./cmd/jiragen update` after reviewing a spec change.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// maxInputSize bounds every input file: specifications are untrusted input.
const maxInputSize = 64 << 20

func readPinned(root, rel, want string) ([]byte, string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return nil, "", err
	}
	if st.Size() > maxInputSize {
		return nil, "", fmt.Errorf("%s: %d bytes exceeds limit of %d", rel, st.Size(), maxInputSize)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	if want != "" && want != got {
		return nil, got, fmt.Errorf("%s: checksum mismatch (manifest %s, file %s); review the change and run `jiragen update`", rel, want, got)
	}
	if want == "" {
		return nil, got, fmt.Errorf("%s: no checksum pinned in manifest; run `jiragen update`", rel)
	}
	return b, got, nil
}
