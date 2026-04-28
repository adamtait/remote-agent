// Package configwrite merges plugin config and secrets into existing config.yaml
// files without clobbering unrelated top-level keys.
package configwrite

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergePluginConfig adds plugins.<name> = pluginCfg and adds entries to
// secrets.<logical> = {secret_name, env_var}. Returns merged YAML.
//
// It preserves all pre-existing top-level keys. If plugins: or secrets: are
// missing they are created. Installing the same plugin twice is idempotent
// (last write wins for that plugin's subtree).
func MergePluginConfig(
	original string,
	pluginName string,
	pluginCfg map[string]any,
	secretNames map[string]string,   // logical name → GCP secret name
	secretEnvVars map[string]string, // logical name → env var name
) (string, error) {
	// Parse original document (or start from empty mapping).
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(original), &root); err != nil {
		return "", fmt.Errorf("configwrite: parse yaml: %w", err)
	}
	if root.Kind == 0 {
		// Completely empty file — synthesise a document node.
		root = yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{Kind: yaml.MappingNode, Tag: "!!map"},
			},
		}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("configwrite: unexpected root node kind %v", root.Kind)
	}
	docBody := root.Content[0]
	if docBody.Kind == yaml.ScalarNode && (docBody.Tag == "!!null" || docBody.Value == "null" || docBody.Value == "~" || docBody.Value == "") {
		// File contained only "null" — replace with empty mapping.
		docBody = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content[0] = docBody
	}
	if docBody.Kind != yaml.MappingNode {
		return "", fmt.Errorf("configwrite: top-level YAML must be a mapping, got kind %v", docBody.Kind)
	}

	// Ensure plugins: section exists.
	pluginsNode := ensureMappingChild(docBody, "plugins")

	// Set / replace plugins.<name> with pluginCfg.
	pluginValNode, err := anyToNode(pluginCfg)
	if err != nil {
		return "", fmt.Errorf("configwrite: encode plugin config: %w", err)
	}
	setMappingKey(pluginsNode, pluginName, pluginValNode)

	// Ensure secrets: section exists.
	secretsNode := ensureMappingChild(docBody, "secrets")

	// Set / replace each secret entry.
	for logical, gcpName := range secretNames {
		envVar := secretEnvVars[logical]
		entryNode := &yaml.Node{
			Kind:  yaml.MappingNode,
			Tag:   "!!map",
			Style: 0,
		}
		appendKV(entryNode, "secret_name", gcpName)
		appendKV(entryNode, "env_var", envVar)
		setMappingKey(secretsNode, logical, entryNode)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("configwrite: marshal: %w", err)
	}
	// yaml.Marshal wraps in a document marker "---\n"; strip it for cleaner output.
	return strings.TrimPrefix(string(out), "---\n"), nil
}

// MergeSecretEntries adds or replaces secrets.<logical> entries in YAML without
// touching the plugins: section. Used when secrets are written to a different
// config file than the plugin config.
func MergeSecretEntries(
	original string,
	secretNames map[string]string,   // logical name → GCP secret name
	secretEnvVars map[string]string, // logical name → env var name
) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(original), &root); err != nil {
		return "", fmt.Errorf("configwrite: parse yaml: %w", err)
	}
	if root.Kind == 0 {
		root = yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{Kind: yaml.MappingNode, Tag: "!!map"},
			},
		}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("configwrite: unexpected root node kind %v", root.Kind)
	}
	docBody := root.Content[0]
	if docBody.Kind == yaml.ScalarNode && (docBody.Tag == "!!null" || docBody.Value == "null" || docBody.Value == "~" || docBody.Value == "") {
		docBody = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content[0] = docBody
	}
	if docBody.Kind != yaml.MappingNode {
		return "", fmt.Errorf("configwrite: top-level YAML must be a mapping, got kind %v", docBody.Kind)
	}

	secretsNode := ensureMappingChild(docBody, "secrets")
	for logical, gcpName := range secretNames {
		envVar := secretEnvVars[logical]
		entryNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendKV(entryNode, "secret_name", gcpName)
		appendKV(entryNode, "env_var", envVar)
		setMappingKey(secretsNode, logical, entryNode)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("configwrite: marshal: %w", err)
	}
	return strings.TrimPrefix(string(out), "---\n"), nil
}

// RemovePluginConfig removes plugins.<name> and the named secrets entries from
// existing YAML, then returns the updated YAML.
func RemovePluginConfig(original, pluginName string, secretLogicals []string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(original), &root); err != nil {
		return "", fmt.Errorf("configwrite: parse yaml: %w", err)
	}
	if root.Kind == 0 || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return original, nil
	}
	docBody := root.Content[0]
	if docBody.Kind != yaml.MappingNode {
		return original, nil
	}

	// Remove plugins.<name>.
	if pluginsNode := findMappingChild(docBody, "plugins"); pluginsNode != nil {
		removeMappingKey(pluginsNode, pluginName)
	}

	// Remove secrets entries.
	if secretsNode := findMappingChild(docBody, "secrets"); secretsNode != nil {
		for _, logical := range secretLogicals {
			removeMappingKey(secretsNode, logical)
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("configwrite: marshal: %w", err)
	}
	return strings.TrimPrefix(string(out), "---\n"), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// ensureMappingChild finds the value node for key in a mapping node,
// creating a new mapping node for it when absent or null.
func ensureMappingChild(parent *yaml.Node, key string) *yaml.Node {
	n := findMappingChild(parent, key)
	if n != nil {
		// Already exists; if it's a null scalar, replace with empty map.
		if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "null" || n.Value == "~" || n.Value == "") {
			n.Kind = yaml.MappingNode
			n.Tag = "!!map"
			n.Value = ""
			n.Content = nil
		}
		return n
	}
	// Not found — append key + empty mapping node.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

// findMappingChild returns the value node for key in a mapping node, or nil.
func findMappingChild(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// setMappingKey sets (or replaces) the value for key in a mapping node.
func setMappingKey(parent *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = val
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	parent.Content = append(parent.Content, keyNode, val)
}

// removeMappingKey removes the key-value pair for key from a mapping node.
func removeMappingKey(parent *yaml.Node, key string) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return
		}
	}
}

// appendKV appends a key: value scalar pair to a mapping node.
func appendKV(parent *yaml.Node, key, value string) {
	kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	vn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	parent.Content = append(parent.Content, kn, vn)
}

// anyToNode encodes an arbitrary Go value into a *yaml.Node.
func anyToNode(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0], nil
	}
	return &doc, nil
}
