package cli

import (
	"fmt"
	"runtime"
)

// Commit and Date describe the build that produced this binary. Like Version
// they are placeholders that release builds overwrite with
// -ldflags "-X github.com/ZHallen122/RegTool/internal/cli.Commit=... -X ...Date=...".
var (
	Commit = "none"
	Date   = "unknown"
)

// versionString is what `regtool version` prints: the version followed by the
// commit, the build date and the platform the binary was built for.
func versionString() string {
	return fmt.Sprintf("regtool %s (%s, built %s, %s/%s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
