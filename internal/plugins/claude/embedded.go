package claude

import _ "embed"

//go:embed assets/install.sh
var embeddedInstall []byte

//go:embed assets/setup.sh
var embeddedSetup []byte
