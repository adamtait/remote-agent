package claude_test

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	claudeplugin "github.com/adamtait/remote-agent/internal/plugins/claude"
)

func boolPtr(b bool) *bool { return &b }

func baseConfig() *config.Config {
	return &config.Config{
		Claude: config.ClaudeConfig{
			Enabled:  boolPtr(true),
			AuthMode: "oauth",
		},
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
		},
	}
}

// ---------- Name ----------

func TestPlugin_Name(t *testing.T) {
	if claudeplugin.New().Name() != "claude" {
		t.Error("expected Name() == \"claude\"")
	}
}

// ---------- Assets ----------

func TestPlugin_Assets_Count(t *testing.T) {
	a := claudeplugin.New().Assets()
	if len(a) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(a))
	}
}

func TestPlugin_Assets_Names(t *testing.T) {
	names := map[string]bool{}
	for _, a := range claudeplugin.New().Assets() {
		names[a.Name] = true
	}
	for _, want := range []string{"claude/install.sh", "claude/setup.sh"} {
		if !names[want] {
			t.Errorf("missing asset %q", want)
		}
	}
}

func TestPlugin_Assets_FileModes(t *testing.T) {
	for _, a := range claudeplugin.New().Assets() {
		if a.FileMode != 0755 {
			t.Errorf("asset %q: want FileMode 0755, got %04o", a.Name, a.FileMode)
		}
	}
}

func TestPlugin_Assets_NonEmpty(t *testing.T) {
	for _, a := range claudeplugin.New().Assets() {
		if len(a.Bytes) == 0 {
			t.Errorf("asset %q is empty", a.Name)
		}
	}
}

func TestPlugin_InstallAsset_HasNpmPackage(t *testing.T) {
	var installBytes []byte
	for _, a := range claudeplugin.New().Assets() {
		if a.Name == "claude/install.sh" {
			installBytes = a.Bytes
			break
		}
	}
	if !bytes.Contains(installBytes, []byte("@anthropic-ai/claude-code")) {
		t.Error("install.sh should reference @anthropic-ai/claude-code")
	}
	if !bytes.Contains(installBytes, []byte("npm install")) {
		t.Error("install.sh should run npm install")
	}
}

func TestPlugin_SetupAsset_InstallsOnFirstBoot(t *testing.T) {
	var b []byte
	for _, a := range claudeplugin.New().Assets() {
		if a.Name == "claude/setup.sh" {
			b = a.Bytes
			break
		}
	}
	if !bytes.Contains(b, []byte("_ra_claude_install_if_missing")) {
		t.Error("setup.sh must call _ra_claude_install_if_missing")
	}
	if !bytes.Contains(b, []byte("CLAUDE_BIN_PATH")) {
		t.Error("setup.sh must check CLAUDE_BIN_PATH before installing")
	}
}

func TestPlugin_SetupAsset_UsesAbsolutePathForVersionAndSmokeTest(t *testing.T) {
	var b []byte
	for _, a := range claudeplugin.New().Assets() {
		if a.Name == "claude/setup.sh" {
			b = a.Bytes
			break
		}
	}
	if !bytes.Contains(b, []byte(`"${CLAUDE_BIN_PATH}"`)) && !bytes.Contains(b, []byte("${CLAUDE_BIN_PATH}")) {
		t.Error("setup.sh should invoke claude via CLAUDE_BIN_PATH (absolute path) for version and smoke test")
	}
	if !bytes.Contains(b, []byte("--version")) {
		t.Error("setup.sh should run claude --version for onboarding seed")
	}
}

func TestPlugin_SetupAsset_HasHandlerFunction(t *testing.T) {
	var b []byte
	for _, a := range claudeplugin.New().Assets() {
		if a.Name == "claude/setup.sh" {
			b = a.Bytes
			break
		}
	}
	if !bytes.Contains(b, []byte("ra_claude_handle_secret")) {
		t.Error("setup.sh must define ra_claude_handle_secret")
	}
}

// ---------- DefaultConfigFragments ----------

func TestPlugin_DefaultConfigFragments(t *testing.T) {
	cfgBlock, secretsBlock := claudeplugin.New().DefaultConfigFragments()
	if !strings.Contains(cfgBlock, "claude:") {
		t.Errorf("default config block should contain 'claude:', got: %q", cfgBlock)
	}
	if !strings.Contains(cfgBlock, "enabled: false") {
		t.Errorf("default config block should have enabled: false, got: %q", cfgBlock)
	}
	if secretsBlock != "" {
		t.Errorf("default secrets block should be empty, got: %q", secretsBlock)
	}
}

// ---------- ContainerEnvPairs ----------

func TestPlugin_ContainerEnvPairs_Enabled(t *testing.T) {
	cfg := baseConfig()
	pairs := claudeplugin.New().ContainerEnvPairs(cfg)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0] != "RA_CLAUDE_ENABLED=true" {
		t.Errorf("expected RA_CLAUDE_ENABLED=true, got %q", pairs[0])
	}
}

func TestPlugin_ContainerEnvPairs_Disabled(t *testing.T) {
	cfg := &config.Config{Claude: config.ClaudeConfig{Enabled: boolPtr(false)}}
	pairs := claudeplugin.New().ContainerEnvPairs(cfg)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0] != "RA_CLAUDE_ENABLED=false" {
		t.Errorf("expected RA_CLAUDE_ENABLED=false, got %q", pairs[0])
	}
}

// ---------- Validate ----------

func TestPlugin_Validate_EnabledOAuth_OK(t *testing.T) {
	errs := claudeplugin.New().Validate(baseConfig())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestPlugin_Validate_EnabledAPIKey_OK(t *testing.T) {
	cfg := &config.Config{
		Claude:  config.ClaudeConfig{Enabled: boolPtr(true), AuthMode: "apikey"},
		Secrets: map[string]config.SecretEntry{"claude_apikey": {SecretName: "s", EnvVar: "ANTHROPIC_API_KEY"}},
	}
	if errs := claudeplugin.New().Validate(cfg); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestPlugin_Validate_Disabled_OK(t *testing.T) {
	cfg := &config.Config{
		Claude:  config.ClaudeConfig{Enabled: boolPtr(false)},
		Secrets: map[string]config.SecretEntry{},
	}
	if errs := claudeplugin.New().Validate(cfg); len(errs) != 0 {
		t.Errorf("expected no errors when disabled, got %v", errs)
	}
}

func TestPlugin_Validate_EnabledMissingSecret(t *testing.T) {
	cfg := baseConfig()
	delete(cfg.Secrets, "claude_oauth")
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "claude_oauth") {
		t.Errorf("expected claude_oauth error, got %v", errs)
	}
}

func TestPlugin_Validate_EnabledMissingAuthMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Claude.AuthMode = ""
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "auth_mode") {
		t.Errorf("expected auth_mode error, got %v", errs)
	}
}

func TestPlugin_Validate_EnabledBadAuthMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Claude.AuthMode = "nope"
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "auth_mode") {
		t.Errorf("expected bad auth_mode error, got %v", errs)
	}
}

func TestPlugin_Validate_EnabledBothSecrets(t *testing.T) {
	cfg := baseConfig()
	cfg.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "k", EnvVar: "ANTHROPIC_API_KEY"}
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "claude_apikey") {
		t.Errorf("expected both-secrets error, got %v", errs)
	}
}

func TestPlugin_Validate_DisabledButSecretPresent(t *testing.T) {
	cfg := &config.Config{
		Claude:  config.ClaudeConfig{Enabled: boolPtr(false)},
		Secrets: map[string]config.SecretEntry{"claude_oauth": {SecretName: "s", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}},
	}
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "claude_oauth") {
		t.Errorf("expected secret-present-but-disabled error, got %v", errs)
	}
}

func TestPlugin_Validate_NilEnabled_WithSecret_MigrationSignal(t *testing.T) {
	cfg := &config.Config{
		Claude:  config.ClaudeConfig{}, // Enabled == nil
		Secrets: map[string]config.SecretEntry{"claude_oauth": {SecretName: "s", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}},
	}
	errs := claudeplugin.New().Validate(cfg)
	if len(errs) == 0 || !containsAny(errs, "claude.enabled is not set") {
		t.Errorf("expected migration-signal error, got %v", errs)
	}
}

// ---------- Prompt ----------

func TestPlugin_Prompt_DisabledByDefault(t *testing.T) {
	// Empty input → default "N" → disabled
	r := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	cfg, secrets, err := claudeplugin.New().Prompt(r, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "enabled: false") {
		t.Errorf("expected enabled: false, got %q", cfg)
	}
	if secrets != "" {
		t.Errorf("expected empty secrets block, got %q", secrets)
	}
}

func TestPlugin_Prompt_EnabledOAuth(t *testing.T) {
	// "y\n" → enabled; "\n" → default OAuth (choice 1)
	r := bufio.NewReader(strings.NewReader("y\n\n"))
	var out bytes.Buffer
	cfg, secrets, err := claudeplugin.New().Prompt(r, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "enabled: true") || !strings.Contains(cfg, "auth_mode: oauth") {
		t.Errorf("expected oauth config block, got %q", cfg)
	}
	if !strings.Contains(secrets, "claude_oauth") {
		t.Errorf("expected claude_oauth in secrets block, got %q", secrets)
	}
}

func TestPlugin_Prompt_EnabledAPIKey(t *testing.T) {
	// "y\n" → enabled; "2\n" → API key
	r := bufio.NewReader(strings.NewReader("y\n2\n"))
	var out bytes.Buffer
	cfg, secrets, err := claudeplugin.New().Prompt(r, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "auth_mode: apikey") {
		t.Errorf("expected apikey config block, got %q", cfg)
	}
	if !strings.Contains(secrets, "claude_apikey") {
		t.Errorf("expected claude_apikey in secrets block, got %q", secrets)
	}
}

func containsAny(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
