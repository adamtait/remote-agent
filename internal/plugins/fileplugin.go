package plugins

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// FilePlugin implements plugin behaviors by reading a plugin directory.
type FilePlugin struct {
	fsys   fs.FS
	schema PluginSchema
}

// LoadPlugin reads plugin.yaml from fsys and returns a FilePlugin.
func LoadPlugin(fsys fs.FS) (*FilePlugin, error) {
	data, err := fs.ReadFile(fsys, "plugin.yaml")
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml: %w", err)
	}
	var s PluginSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("plugin.yaml: name is required")
	}
	return &FilePlugin{fsys: fsys, schema: s}, nil
}

// Name returns the plugin's canonical identifier.
func (p *FilePlugin) Name() string { return p.schema.Name }

// DefaultConfigFragments returns the zero-value config YAML block for this plugin.
func (p *FilePlugin) DefaultConfigFragments() (configBlock string, secretsBlock string) {
	defaults := make(map[string]interface{})
	for _, f := range p.schema.Config {
		if f.Default != nil {
			defaults[f.Name] = f.Default
		} else {
			switch f.Type {
			case "bool":
				defaults[f.Name] = false
			case "int":
				defaults[f.Name] = 0
			case "string_list":
				defaults[f.Name] = []string{}
			default:
				defaults[f.Name] = ""
			}
		}
	}
	return buildPluginsConfigBlock(p.schema.Name, defaults), ""
}

// containerEnvPairsFrom generates RA_PLUGIN_<NAME>_<FIELD>=<value> pairs from pluginCfg.
func (p *FilePlugin) containerEnvPairsFrom(pluginCfg map[string]interface{}) []string {
	prefix := "RA_PLUGIN_" + strings.ToUpper(strings.ReplaceAll(p.schema.Name, "-", "_")) + "_"
	var pairs []string
	for _, f := range p.schema.Config {
		key := prefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		v, ok := pluginCfg[f.Name]
		if !ok {
			pairs = append(pairs, key+"=")
			continue
		}
		var val string
		switch typed := v.(type) {
		case []string:
			val = strings.Join(typed, " ")
		case []interface{}:
			// yaml.v3 decodes YAML sequences into []interface{} when target is any
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				parts = append(parts, fmt.Sprintf("%v", item))
			}
			val = strings.Join(parts, " ")
		default:
			val = fmt.Sprintf("%v", v)
		}
		pairs = append(pairs, key+"="+val)
	}
	if len(p.schema.AuthProviders) > 0 {
		provider, _ := pluginCfg["auth_provider"].(string)
		pairs = append(pairs, prefix+"AUTH_PROVIDER="+provider)
	}
	return pairs
}

// validateConfig checks required fields and auth_provider against pluginCfg.
// Returns nil when the plugin is disabled (enabled==false or absent).
func (p *FilePlugin) validateConfig(pluginCfg map[string]interface{}) []string {
	for _, f := range p.schema.Config {
		if f.Name == "enabled" {
			if v, ok := pluginCfg["enabled"]; !ok || v != true {
				return nil
			}
		}
	}

	var errs []string
	for _, f := range p.schema.Config {
		if f.Required {
			v, ok := pluginCfg[f.Name]
			if !ok {
				errs = append(errs, fmt.Sprintf("plugin %s: required config field %q is missing", p.schema.Name, f.Name))
				continue
			}
			if f.Type == "string_list" {
				empty := false
				switch typed := v.(type) {
				case []string:
					empty = len(typed) == 0
				case []interface{}:
					empty = len(typed) == 0
				}
				if empty {
					errs = append(errs, fmt.Sprintf("plugin %s: required config field %q must not be empty", p.schema.Name, f.Name))
				}
			}
		}
	}

	if len(p.schema.AuthProviders) > 0 {
		provider, _ := pluginCfg["auth_provider"].(string)
		if provider == "" {
			errs = append(errs, fmt.Sprintf("plugin %s: auth_provider must be set when auth_providers are defined", p.schema.Name))
		} else {
			valid := false
			for _, ap := range p.schema.AuthProviders {
				if ap.Name == provider {
					valid = true
					break
				}
			}
			if !valid {
				errs = append(errs, fmt.Sprintf("plugin %s: unknown auth_provider %q", p.schema.Name, provider))
			}
		}
	}
	return errs
}

// PromptResult is what FilePlugin.Prompt returns: the structured values
// the caller needs to (a) upload secrets to GCP Secret Manager and
// (b) write config blocks to config.yaml.
type PromptResult struct {
	Config       map[string]any    // field name → value
	AuthProvider *AuthProvider     // nil if plugin has none
	SecretValues map[string]string // logical secret name → plaintext value
}

// Prompt interactively collects plugin config + auth provider + secret
// values from the user. Returns a structured result; the caller renders
// YAML and uploads secrets.
func (p *FilePlugin) Prompt(r *bufio.Reader, out io.Writer, stderr io.Writer, stdin *os.File) (*PromptResult, error) {
	pluginCfg := make(map[string]any)
	for _, f := range p.schema.Config {
		// `ra plugin install` always enables the new plugin; users can
		// flip `enabled: false` in config.yaml to disable later.
		if f.Name == "enabled" {
			pluginCfg["enabled"] = true
			continue
		}
		if f.ValueCommand != nil {
			val, e := runValueCommand(*f.ValueCommand, stderr, stdin)
			if e != nil {
				return nil, fmt.Errorf("collecting config field %s: %w", f.Name, e)
			}
			pluginCfg[f.Name] = val
			continue
		}
		val, e := promptField(r, out, f)
		if e != nil {
			return nil, e
		}
		pluginCfg[f.Name] = val
	}

	var activeProvider *AuthProvider
	if len(p.schema.AuthProviders) > 0 {
		ap, e := promptAuthProvider(r, out, p.schema.AuthProviders)
		if e != nil {
			return nil, e
		}
		pluginCfg["auth_provider"] = ap.Name
		activeProvider = ap
	}

	values := make(map[string]string)
	for _, s := range p.schema.Secrets {
		v, e := CollectSecretValue(stdin, stderr, s)
		if e != nil {
			return nil, e
		}
		values[s.Name] = v
	}
	if activeProvider != nil {
		for _, s := range activeProvider.Secrets {
			v, e := CollectSecretValue(stdin, stderr, s)
			if e != nil {
				return nil, e
			}
			values[s.Name] = v
		}
	}

	return &PromptResult{
		Config:       pluginCfg,
		AuthProvider: activeProvider,
		SecretValues: values,
	}, nil
}

// CollectSecretValue collects one secret's plaintext value, dispatching
// to the schema's value_command (interactive CLI capture, e.g. `codex
// login`) when defined, or to a hidden text prompt otherwise. Used by
// `ra plugin install` and by `ra create` pre-flight when a referenced
// secret is missing from the target gcloud project.
func CollectSecretValue(stdin *os.File, stderr io.Writer, s SecretDef) (string, error) {
	if s.ValueCommand != nil {
		return runValueCommand(*s.ValueCommand, stderr, stdin)
	}
	return promptSecretValue(stdin, stderr, s)
}

func runValueCommand(vc ValueCommand, stderr io.Writer, stdin *os.File) (string, error) {
	if vc.Command == "" {
		return "", fmt.Errorf("value_command.command is required")
	}
	outputMode := vc.Output
	if outputMode == "" {
		outputMode = "stdout"
	}

	cmd := exec.Command(vc.Command, vc.Args...)
	if vc.Interactive {
		if stdin != nil {
			cmd.Stdin = stdin
		} else {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("running %s: %w", vc.Command, err)
		}
	} else {
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, err := cmd.Output()
		if err != nil {
			msg := strings.TrimSpace(errBuf.String())
			if msg != "" {
				return "", fmt.Errorf("running %s: %w: %s", vc.Command, err, msg)
			}
			return "", fmt.Errorf("running %s: %w", vc.Command, err)
		}
		if outputMode == "stdout" {
			value := strings.TrimSpace(string(out))
			if value == "" {
				return "", fmt.Errorf("value_command produced empty stdout")
			}
			return value, nil
		}
	}

	switch outputMode {
	case "stdout":
		return "", fmt.Errorf("interactive stdout capture is not supported")
	case "file_base64":
		path := expandUserPath(vc.Path)
		if path == "" {
			return "", fmt.Errorf("value_command.path is required for file_base64 output")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", path, err)
		}
		if len(data) == 0 {
			return "", fmt.Errorf("reading %s: file is empty", path)
		}
		if err := validateValueCommandData(vc, data, path); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", fmt.Errorf("unsupported value_command.output %q", outputMode)
	}
}

func validateValueCommandData(vc ValueCommand, data []byte, label string) error {
	switch vc.Validate {
	case "", "none":
		return nil
	case "json":
		var tmp any
		if err := json.Unmarshal(data, &tmp); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", label, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported value_command.validate %q", vc.Validate)
	}
}

func expandUserPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

// promptSecretValue reads a plaintext secret value with hidden input.
func promptSecretValue(stdin *os.File, stderr io.Writer, s SecretDef) (string, error) {
	fmt.Fprintf(stderr, "Enter value for secret %q (env var %s): ", s.Name, s.EnvVar)
	val, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("reading secret %s: %w", s.Name, err)
	}
	v := strings.TrimSpace(string(val))
	if v == "" {
		return "", fmt.Errorf("secret %s cannot be empty", s.Name)
	}
	return v, nil
}

// RenderConfigBlock returns the YAML fragment for plugins.<name>.* given
// a PromptResult — used by `ra plugin install` when writing to config.
func (p *FilePlugin) RenderConfigBlock(res *PromptResult) string {
	return buildPluginsConfigBlock(p.schema.Name, res.Config)
}

// RenderSecretsBlock returns the YAML fragment for the secrets: section
// (indented entries, no root key) for the secrets relevant to res.
func (p *FilePlugin) RenderSecretsBlock(res *PromptResult) string {
	return buildSecretsBlock(p.schema.Secrets, res.AuthProvider)
}

func promptField(r *bufio.Reader, out io.Writer, f ConfigField) (interface{}, error) {
	switch f.Type {
	case "bool":
		if b, ok := f.Default.(bool); ok && b {
			fmt.Fprintf(out, "%s [Y/n]: ", f.Description)
		} else {
			fmt.Fprintf(out, "%s [y/N]: ", f.Description)
		}
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "" {
			if s, ok := f.Default.(bool); ok {
				return s, nil
			}
			return false, nil
		}
		return choice == "y" || choice == "yes", nil
	case "int":
		fmt.Fprintf(out, "%s: ", f.Description)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		var v int
		fmt.Sscan(strings.TrimSpace(line), &v)
		return v, nil
	case "string_list":
		fmt.Fprintf(out, "%s\n(enter one URL per line, empty line to finish):\n", f.Description)
		var urls []string
		for {
			fmt.Fprintf(out, "  URL [%d]: ", len(urls)+1)
			line, err := r.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, err
			}
			v := strings.TrimSpace(line)
			if v == "" {
				break
			}
			urls = append(urls, v)
		}
		return urls, nil
	default: // string
		if len(f.Options) > 0 {
			return promptOptions(r, out, f)
		}
		defVal, _ := f.Default.(string)
		fmt.Fprintf(out, "%s [%s]: ", f.Description, defVal)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			return defVal, nil
		}
		return v, nil
	}
}

func promptOptions(r *bufio.Reader, out io.Writer, f ConfigField) (string, error) {
	fmt.Fprintf(out, "%s:\n", f.Description)
	for i, o := range f.Options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, o)
	}
	fmt.Fprintf(out, "choice [1-%d] [1]: ", len(f.Options))
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	var idx int
	if _, e := fmt.Sscan(strings.TrimSpace(line), &idx); e != nil || idx < 1 || idx > len(f.Options) {
		idx = 1
	}
	return f.Options[idx-1], nil
}

func promptAuthProvider(r *bufio.Reader, out io.Writer, providers []AuthProvider) (*AuthProvider, error) {
	fmt.Fprintln(out, "Auth provider:")
	for i, ap := range providers {
		fmt.Fprintf(out, "  %d) %s\n", i+1, ap.Description)
	}
	fmt.Fprintf(out, "choice [1-%d] [1]: ", len(providers))
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	var idx int
	if _, e := fmt.Sscan(strings.TrimSpace(line), &idx); e != nil || idx < 1 || idx > len(providers) {
		idx = 1
	}
	return &providers[idx-1], nil
}

func buildPluginsConfigBlock(name string, cfg map[string]interface{}) string {
	out, _ := yaml.Marshal(map[string]interface{}{
		"plugins": map[string]interface{}{name: cfg},
	})
	return string(out)
}

// FS returns the filesystem rooted at the plugin's directory. Used by
// callers (e.g. the build-context assembler) that need to walk plugin-
// contributed asset directories.
func (p *FilePlugin) FS() fs.FS { return p.fsys }

// Schema returns the plugin's parsed schema.
func (p *FilePlugin) Schema() PluginSchema { return p.schema }

// Validate checks required config fields and auth_provider validity.
func (p *FilePlugin) Validate(cfg *config.Config) []string {
	pluginCfg := map[string]interface{}{}
	if cfg != nil && cfg.Plugins != nil {
		if c, ok := cfg.Plugins[p.schema.Name]; ok {
			pluginCfg = c
		}
	}
	return p.validateConfig(pluginCfg)
}

// ContainerEnvPairs returns RA_PLUGIN_<NAME>_<FIELD>=<value> pairs for --container-env.
func (p *FilePlugin) ContainerEnvPairs(cfg *config.Config) []string {
	pluginCfg := map[string]interface{}{}
	if cfg != nil && cfg.Plugins != nil {
		if c, ok := cfg.Plugins[p.schema.Name]; ok {
			pluginCfg = c
		}
	}
	return p.containerEnvPairsFrom(pluginCfg)
}

// buildSecretsBlock returns indented YAML entries (no root key) for injection into a
// secrets: block. Merges top-level secrets with provider-specific secrets.
func buildSecretsBlock(topLevel []SecretDef, provider *AuthProvider) string {
	var defs []SecretDef
	defs = append(defs, topLevel...)
	if provider != nil {
		defs = append(defs, provider.Secrets...)
	}
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, d := range defs {
		gcp := d.DefaultSecretName
		if gcp == "" {
			gcp = d.Name
		}
		fmt.Fprintf(&sb, "  %s:\n    secret_name: %s\n    env_var: %s\n", d.Name, gcp, d.EnvVar)
	}
	return sb.String()
}
