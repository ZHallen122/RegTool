package service

import (
	"github.com/ZHallen122/RegTool/internal/backend"
	"github.com/ZHallen122/RegTool/source/structs"
)

// switcher is one thing regtool can point at a mirror. Almost every switcher
// is a [backend.Backend] that rewrites a configuration file; homebrew is the
// exception, because it is configured through environment variables and needs
// the brew command itself. The interface hides that difference from Service.
type switcher interface {
	// Name is the stable identifier of the switcher, for example "npm".
	Name() string
	// Detect reports whether the tool appears to be used on this machine.
	Detect() (bool, error)
	// Current returns the registry URL the tool is configured with, or ""
	// when it is not configured at all.
	Current() (string, error)
	// Plan computes the change that points the tool at a region's mirrors
	// without performing it. url is the switcher's own mirror, already looked
	// up in regionSources; the whole map is passed along because homebrew
	// needs four more keys out of it.
	Plan(url string, regionSources structs.RegistryRegionSources) (change, error)
}

// change is a computed, not yet performed, switch. It is what makes a dry run
// possible: the caller can inspect it, snapshot the files it lists and only
// then apply it.
type change interface {
	// From is the registry the tool points at now, "" when unset.
	From() string
	// To is the registry the change would configure.
	To() string
	// Paths lists every file the change would write, so they can be
	// snapshotted before it is applied.
	Paths() []string
	// Diff renders the change for a human, "" for a no-op.
	Diff() string
	// IsNoop reports whether applying the change would leave everything as it
	// is.
	IsNoop() bool
	// Apply performs the change.
	Apply() error
}

// fileSwitcher adapts a file-editing backend to the switcher interface.
type fileSwitcher struct{ backend backend.Backend }

func (f fileSwitcher) Name() string             { return f.backend.Name() }
func (f fileSwitcher) Detect() (bool, error)    { return f.backend.Detect() }
func (f fileSwitcher) Current() (string, error) { return f.backend.Current() }

func (f fileSwitcher) Plan(url string, _ structs.RegistryRegionSources) (change, error) {
	plan, err := f.backend.Plan(url)
	if err != nil {
		return nil, err
	}
	return filePlan{plan}, nil
}

// filePlan adapts a backend plan to the change interface.
type filePlan struct{ plan *backend.Plan }

func (c filePlan) From() string    { return c.plan.From }
func (c filePlan) To() string      { return c.plan.To }
func (c filePlan) Paths() []string { return []string{c.plan.Path} }
func (c filePlan) Diff() string    { return c.plan.Diff() }
func (c filePlan) IsNoop() bool    { return c.plan.IsNoop() }
func (c filePlan) Apply() error    { return c.plan.Apply() }
