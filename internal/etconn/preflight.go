package etconn

import (
	"fmt"
	"os/exec"
)

// etLookPath is indirected so tests can simulate `et` being present or absent
// without mutating PATH.
var etLookPath = exec.LookPath

// EnsureETInstalled returns an actionable error if the local `et` (Eternal
// Terminal) client is not on PATH. It is cheap and is called before the
// workstation is resolved/started, so the user is not made to wait through a
// cold start only to discover the client is missing.
func EnsureETInstalled() error {
	if _, err := etLookPath("et"); err != nil {
		return fmt.Errorf("the Eternal Terminal client `et` was not found on PATH.\n" +
			"  Install it locally:   brew install et   (see https://eternalterminal.dev/)\n" +
			"  The workstation also needs the et server: install the `et` ra-plugin and re-run `ra create`.")
	}
	return nil
}
