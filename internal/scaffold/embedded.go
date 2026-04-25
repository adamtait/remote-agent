// Package scaffold — embedded.go
// Canonical default build assets, compiled into the ra binary.
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed assets/profile.d/00-ra-wait.sh
var embeddedWaitScript []byte

//go:embed assets/claude/install.sh
var embeddedClaudeInstall []byte

//go:embed assets/claude/setup.sh
var embeddedClaudeSetup []byte
