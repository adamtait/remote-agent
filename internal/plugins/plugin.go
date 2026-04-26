// Package plugins defines the Plugin interface and Asset type.
package plugins

import (
	"bufio"
	"io"
	"os"

	"github.com/adamtait/remote-agent/internal/config"
)

// Asset describes a single build asset contributed by a plugin.
type Asset struct {
	Name     string
	Bytes    []byte
	FileMode os.FileMode
}

// Plugin is the contract every optional capability must satisfy.
type Plugin interface {
	// Name returns the plugin's canonical identifier.
	Name() string

	// Assets returns build assets this plugin contributes to the image.
	Assets() []Asset

	// Validate returns human-readable errors if the plugin's config is invalid.
	Validate(cfg *config.Config) []string

	// Prompt interactively collects plugin config from the user.
	// Returns YAML fragments to inject into the rendered user config:
	// configBlock goes into the plugin's top-level config section,
	// secretsBlock is indented entries under the secrets: map.
	Prompt(r *bufio.Reader, out io.Writer) (configBlock string, secretsBlock string, err error)

	// DefaultConfigFragments returns the template/zero-value YAML fragments
	// used by non-interactive commands (ra setup template).
	DefaultConfigFragments() (configBlock string, secretsBlock string)

	// ContainerEnvPairs returns KEY=VALUE pairs to append to --container-env.
	ContainerEnvPairs(cfg *config.Config) []string
}
