// Package claude implements the Claude Code plugin.
package claude

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/plugins"
)

// Plugin implements plugins.Plugin for Claude Code.
type Plugin struct{}

// New returns a new Claude Code plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "claude" }

func (p *Plugin) Assets() []plugins.Asset {
	return []plugins.Asset{
		{Name: "claude/install.sh", Bytes: embeddedInstall, FileMode: 0755},
		{Name: "claude/setup.sh", Bytes: embeddedSetup, FileMode: 0755},
	}
}

func (p *Plugin) Validate(cfg *config.Config) []string {
	return validateClaude(cfg)
}

// Prompt asks the user whether to enable Claude Code and, if so, which auth
// mode to use. Returns the YAML config block and secrets block to inject.
func (p *Plugin) Prompt(r *bufio.Reader, out io.Writer) (string, string, error) {
	enabled, err := promptClaudeEnabled(r, out)
	if err != nil {
		return "", "", err
	}
	if !enabled {
		return "claude:\n  enabled: false\n", "", nil
	}
	mode, err := promptClaudeAuthMode(r, out)
	if err != nil {
		return "", "", err
	}
	return buildFragments(mode)
}

func (p *Plugin) DefaultConfigFragments() (string, string) {
	return "claude:\n  enabled: false\n", ""
}

func (p *Plugin) ContainerEnvPairs(cfg *config.Config) []string {
	return []string{"RA_CLAUDE_ENABLED=" + strconv.FormatBool(cfg.Claude.IsEnabled())}
}

// buildFragments returns the config and secrets YAML blocks for the given auth mode.
func buildFragments(mode string) (configBlock string, secretsBlock string, err error) {
	switch mode {
	case "apikey":
		return "claude:\n  enabled: true\n  auth_mode: apikey\n",
			"  claude_apikey:\n    secret_name: anthropic-api-key\n    env_var: ANTHROPIC_API_KEY\n",
			nil
	default: // "oauth"
		return "claude:\n  enabled: true\n  auth_mode: oauth\n",
			"  claude_oauth:\n    secret_name: claude-code-oauth-token\n    env_var: CLAUDE_CODE_OAUTH_TOKEN\n    # oauth_account_secret_name: claude-oauth-account   # optional\n",
			nil
	}
}

func promptClaudeEnabled(r *bufio.Reader, out io.Writer) (bool, error) {
	fmt.Fprintf(out, "Enable Claude Code? (installs @anthropic-ai/claude-code into the image) [y/N] [N]: ")
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	choice := strings.ToLower(strings.TrimSpace(line))
	return choice == "y" || choice == "yes", nil
}

func promptClaudeAuthMode(r *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Claude auth mode:")
	fmt.Fprintln(out, "  1) OAuth token (recommended)")
	fmt.Fprintln(out, "  2) API key (via apiKeyHelper)")
	fmt.Fprintf(out, "choice [1/2] [1]: ")
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if strings.TrimSpace(line) == "2" {
		return "apikey", nil
	}
	return "oauth", nil
}

func validateClaude(cfg *config.Config) []string {
	var errs []string
	_, hasOAuth := cfg.Secrets["claude_oauth"]
	_, hasAPIKey := cfg.Secrets["claude_apikey"]

	if cfg.Claude.Enabled == nil && (hasOAuth || hasAPIKey) {
		errs = append(errs, "claude.enabled is not set but claude_* secrets are configured; "+
			"add 'claude:\\n  enabled: true\\n  auth_mode: oauth|apikey' to ~/.config/ra/config.yaml "+
			"or run 'ra setup --force' to regenerate")
		return errs
	}

	if cfg.Claude.IsEnabled() {
		switch cfg.Claude.AuthMode {
		case "oauth":
			if !hasOAuth {
				errs = append(errs, "claude.enabled=true with auth_mode=oauth requires secrets.claude_oauth")
			}
			if hasAPIKey {
				errs = append(errs, "claude.auth_mode=oauth but secrets.claude_apikey is also set; remove one")
			}
		case "apikey":
			if !hasAPIKey {
				errs = append(errs, "claude.enabled=true with auth_mode=apikey requires secrets.claude_apikey")
			}
			if hasOAuth {
				errs = append(errs, "claude.auth_mode=apikey but secrets.claude_oauth is also set; remove one")
			}
		case "":
			errs = append(errs, "claude.auth_mode is required when claude.enabled=true (oauth or apikey)")
		default:
			errs = append(errs, fmt.Sprintf("claude.auth_mode must be \"oauth\" or \"apikey\", got %q", cfg.Claude.AuthMode))
		}
		return errs
	}

	if hasOAuth {
		errs = append(errs, "secrets.claude_oauth is set but claude.enabled is not true; set claude.enabled=true (auth_mode=oauth) or remove the secret")
	}
	if hasAPIKey {
		errs = append(errs, "secrets.claude_apikey is set but claude.enabled is not true; set claude.enabled=true (auth_mode=apikey) or remove the secret")
	}
	return errs
}

// Ensure Plugin implements plugins.Plugin at compile time.
var _ plugins.Plugin = (*Plugin)(nil)
