package plugins

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"strings"

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

// Assets returns install.sh and setup.sh if present, named plugins/<name>/<script>.
func (p *FilePlugin) Assets() []Asset {
	var assets []Asset
	for _, script := range []string{"install.sh", "setup.sh"} {
		b, err := fs.ReadFile(p.fsys, script)
		if err != nil {
			continue
		}
		assets = append(assets, Asset{
			Name:     "plugins/" + p.schema.Name + "/" + script,
			Bytes:    b,
			FileMode: 0755,
		})
	}
	return assets
}

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
		val := fmt.Sprintf("%v", pluginCfg[f.Name])
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
			if v, ok := pluginCfg["enabled"]; ok && v != true {
				return nil
			}
		}
	}

	var errs []string
	for _, f := range p.schema.Config {
		if f.Required {
			if _, ok := pluginCfg[f.Name]; !ok {
				errs = append(errs, fmt.Sprintf("plugin %s: required config field %q is missing", p.schema.Name, f.Name))
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

// Prompt interactively collects plugin config from the user and returns YAML fragments.
func (p *FilePlugin) Prompt(r *bufio.Reader, out io.Writer) (configBlock string, secretsBlock string, err error) {
	pluginCfg := make(map[string]interface{})

	for _, f := range p.schema.Config {
		val, e := promptField(r, out, f)
		if e != nil {
			return "", "", e
		}
		pluginCfg[f.Name] = val
	}

	enabled := true
	for _, f := range p.schema.Config {
		if f.Name == "enabled" {
			if v, ok := pluginCfg["enabled"]; ok {
				enabled = v == true
			}
		}
	}

	var activeProvider *AuthProvider
	if enabled && len(p.schema.AuthProviders) > 0 {
		ap, e := promptAuthProvider(r, out, p.schema.AuthProviders)
		if e != nil {
			return "", "", e
		}
		pluginCfg["auth_provider"] = ap.Name
		activeProvider = ap
	}

	return buildPluginsConfigBlock(p.schema.Name, pluginCfg),
		buildSecretsBlock(p.schema.Secrets, activeProvider),
		nil
}

func promptField(r *bufio.Reader, out io.Writer, f ConfigField) (interface{}, error) {
	switch f.Type {
	case "bool":
		def := "N"
		if b, ok := f.Default.(bool); ok && b {
			def = "Y"
		}
		fmt.Fprintf(out, "%s [y/N] [%s]: ", f.Description, def)
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
