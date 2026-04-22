// Package scaffold — embedded.go
// Canonical default build assets, compiled into the ws binary.
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/entrypoint.sh
var embeddedEntrypoint []byte
