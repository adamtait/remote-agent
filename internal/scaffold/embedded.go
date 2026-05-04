// Package scaffold — embedded.go
// Canonical default build assets, compiled into the ra binary.
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/Dockerfile.d/120_atelier-extension.sh
var embeddedAtelierExtensionHook []byte

//go:embed assets/workstation-startup.d/200_remote-agent-setup.sh
var embeddedRemoteAgentSetup []byte

//go:embed assets/profile.d/00-ra-wait.sh
var embeddedWaitScript []byte
