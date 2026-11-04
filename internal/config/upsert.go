package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// InstanceUpdate is what `jiracli config setup` sets on one instance. Other
// settings of an existing instance (timeouts, proxy, mTLS, ...) are kept.
type InstanceUpdate struct {
	Name          string
	BaseURL       string
	CredentialRef string
	ReadOnly      bool
	CABundle      string // "" removes tls.ca_bundle
	MakeDefault   bool
}

// UpsertInstance creates the config file (and its folder) if needed, then
// adds or updates one instance in place, keeping comments, other instances
// and unrelated settings. The result is validated before anything is
// written; on error the file is unchanged.
func UpsertInstance(path string, u InstanceUpdate) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		b, err = []byte("version: 1\ninstances: {}\n"), nil
	}
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a YAML mapping", path)
	}
	root := doc.Content[0]
	instances := child(root, "instances", yaml.MappingNode)
	if instances.Style == yaml.FlowStyle { // "instances: {}" of a new file
		instances.Style = 0
	}
	in := child(instances, u.Name, yaml.MappingNode)
	if _, ok := lookup(in, "deployment"); !ok {
		set(in, "deployment", "datacenter")
	}
	set(in, "base_url", u.BaseURL)
	auth := child(in, "auth", yaml.MappingNode)
	if _, ok := lookup(auth, "type"); !ok {
		set(auth, "type", "pat")
	}
	set(auth, "credential_ref", u.CredentialRef)
	if u.ReadOnly {
		setTag(in, "read_only", "true", "!!bool")
	} else {
		remove(in, "read_only")
	}
	if u.CABundle != "" {
		set(child(in, "tls", yaml.MappingNode), "ca_bundle", u.CABundle)
	} else if tls, ok := lookup(in, "tls"); ok {
		remove(tls, "ca_bundle")
		if len(tls.Content) == 0 {
			remove(in, "tls")
		}
	}
	if _, ok := lookup(root, "version"); !ok {
		setTag(root, "version", "1", "!!int")
	}
	if d, ok := lookup(root, "default_instance"); u.MakeDefault || !ok || d.Value == "" {
		set(root, "default_instance", u.Name)
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if _, err := Parse(out.Bytes(), path); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func lookup(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], true
		}
	}
	return nil, false
}

// child returns m[key], creating it with kind when missing.
func child(m *yaml.Node, key string, kind yaml.Kind) *yaml.Node {
	if v, ok := lookup(m, key); ok && v.Kind == kind {
		return v
	}
	v := &yaml.Node{Kind: kind}
	setNode(m, key, v)
	return v
}

func set(m *yaml.Node, key, value string) { setTag(m, key, value, "!!str") }

func setTag(m *yaml.Node, key, value, tag string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

func setNode(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v.HeadComment, v.LineComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

func remove(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
