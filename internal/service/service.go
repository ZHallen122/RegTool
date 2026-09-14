// Package service holds the registry operations shared by the CLI and the
// TUI. Everything here returns plain data structures: nothing in this package
// prints, exits, or reads from the terminal, so both front ends can render the
// results however they like.
//
// A change goes through three stages. Every selected backend is asked for a
// [change], which reads its configuration file and computes the new bytes
// without writing anything; a dry run stops here and shows the diffs. For a
// real run every file about to be touched is copied into one snapshot, and
// only then are the changes applied, so [Service.Undo] can put every file back
// the way it was even if the run failed halfway through.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ZHallen122/RegTool/internal/backend"
	"github.com/ZHallen122/RegTool/internal/history"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/localdata"
	"github.com/ZHallen122/RegTool/source/structs"
)

// LocalRegion is the region reported for a registry URL that does not match
// any of the known mirrors.
const LocalRegion = "local"

// appAliases maps the other names people use for a package manager onto the
// backend name.
var appAliases = map[string]string{
	"brew":      homebrewName,
	"berry":     "yarn-berry",
	"yarn2":     "yarn-berry",
	"yarnberry": "yarn-berry",
	"golang":    "go",
	"rubygems":  "gem",
	"pip3":      "pip",
}

// canonicalApp resolves a user supplied app name to a backend name.
func canonicalApp(app string) string {
	name := strings.ToLower(strings.TrimSpace(app))
	if primary, ok := appAliases[name]; ok {
		return primary
	}
	return name
}

// sourceKey maps a backend name onto the key its mirrors live under in the
// sources file. Both yarn backends share yarn's mirrors, and homebrew is
// represented by its bottle domain because it has no single registry.
func sourceKey(name string) string {
	switch name {
	case "yarn-berry":
		return "yarn"
	case homebrewName:
		return homebrewBottleKey
	default:
		return name
	}
}

// AppStatus describes the registry a single installed app currently points at.
type AppStatus struct {
	// App is the name of the package manager.
	App string `json:"app"`
	// Region is the region whose mirror matches URL, or LocalRegion when the
	// URL is not one the tool knows about.
	Region string `json:"region"`
	// URL is the registry the app is currently configured with.
	URL string `json:"url"`
	// Err is set when the current registry could not be read. The rest of the
	// statuses are still returned so a single broken backend does not hide the
	// others.
	Err error `json:"-"`
}

// MarshalJSON renders Err as a plain string so the struct survives --json.
func (s AppStatus) MarshalJSON() ([]byte, error) {
	type plain AppStatus
	return json.Marshal(struct {
		plain
		Error string `json:"error,omitempty"`
	}{plain(s), errorText(s.Err)})
}

// RegistryEntry is one known mirror: an app, the region it belongs to and the
// URL to use.
type RegistryEntry struct {
	App    string `json:"app"`
	Region string `json:"region"`
	URL    string `json:"url"`
}

// ChangeResult is the outcome of pointing a single app at a new region.
type ChangeResult struct {
	// App is the name of the package manager.
	App string `json:"app"`
	// Path is the configuration file the change writes, empty when the change
	// could not be computed at all.
	Path string `json:"path,omitempty"`
	// From is the registry the app pointed at before the change, empty when it
	// was not configured.
	From string `json:"from"`
	// To is the registry the app now points at, or would point at for a dry
	// run.
	To string `json:"to"`
	// Noop reports that the app already points at To, so nothing was written.
	Noop bool `json:"noop"`
	// Diff is the unified diff of the configuration file, empty for a no-op.
	Diff string `json:"diff,omitempty"`
	// Err records a failure for this app only; the other apps are still
	// attempted.
	Err error `json:"-"`
}

// MarshalJSON renders Err as a plain string so the struct survives --json.
func (c ChangeResult) MarshalJSON() ([]byte, error) {
	type plain ChangeResult
	return json.Marshal(struct {
		plain
		Error string `json:"error,omitempty"`
	}{plain(c), errorText(c.Err)})
}

// UseResult is the outcome of a whole `use` run.
type UseResult struct {
	// Changes has one entry per selected app, in a stable order.
	Changes []ChangeResult `json:"changes"`
	// SnapshotID identifies the snapshot taken before anything was written. It
	// is empty for a dry run and for a run that had nothing to do.
	SnapshotID string `json:"snapshotId,omitempty"`
	// DryRun reports that nothing was written.
	DryRun bool `json:"dryRun"`
}

// Err folds the per-app failures into a single error.
func (r *UseResult) Err() error {
	var errs []error
	for _, outcome := range r.Changes {
		if outcome.Err != nil {
			errs = append(errs, outcome.Err)
		}
	}
	return errors.Join(errs...)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Service runs registry operations against a fixed set of mirrors, a fixed set
// of package manager backends and one snapshot store. All three are injected
// so tests can point the whole thing at a temporary directory.
type Service struct {
	sources   *structs.RegistrySources
	switchers []switcher
	history   *history.Store
}

// An Option tweaks how a Service is built.
type Option func(*Service)

// WithHomebrew adds the homebrew switcher. It is not part of the backend list
// because homebrew is the one package manager regtool still drives by running
// commands instead of by editing a configuration file.
func WithHomebrew() Option {
	return func(s *Service) { s.switchers = append(s.switchers, newHomebrewSwitcher()) }
}

// New builds a Service from an already loaded set of mirrors, the backends it
// should operate on and the store its snapshots go to.
func New(sources *structs.RegistrySources, backends []backend.Backend, store *history.Store, opts ...Option) *Service {
	if sources == nil {
		sources = &structs.RegistrySources{}
	}
	svc := &Service{sources: sources, history: store}
	for _, b := range backends {
		svc.switchers = append(svc.switchers, fileSwitcher{backend: b})
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Load builds the Service the commands run against: the mirrors reachable in
// ctx, every file backend this build knows about plus homebrew, and the
// snapshot store under the user's configuration directory.
func Load(ctx context.Context) (*Service, error) {
	sources, err := source.LoadRegistrySources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load registry sources: %w", err)
	}
	store, err := history.Default()
	if err != nil {
		return nil, fmt.Errorf("failed to locate the snapshot history: %w", err)
	}
	return New(sources, backend.All(backend.DefaultEnv()), store, WithHomebrew()), nil
}

// Status reports, for every app that is installed, the registry it currently
// points at and the region that registry belongs to. Per-app failures are
// carried in AppStatus.Err; the returned error is reserved for a cancelled
// context and for a backend that could not even be detected.
func (s *Service) Status(ctx context.Context) ([]AppStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	installed, err := s.detected()
	if err != nil {
		return nil, err
	}

	out := make([]AppStatus, 0, len(installed))
	for _, sw := range installed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		status := AppStatus{App: sw.Name(), Region: LocalRegion}
		current, err := sw.Current()
		if err != nil {
			status.Err = fmt.Errorf("failed to read the current %s registry: %w", sw.Name(), err)
			out = append(out, status)
			continue
		}

		status.URL = current
		if region, ok := s.matchRegion(sourceKey(sw.Name()), current); ok {
			status.Region = region
		}
		out = append(out, status)
	}
	return out, nil
}

// List returns every known mirror, sorted by app then region. An empty app
// lists them all; otherwise only that app's mirrors are returned and an unknown
// app is an error.
func (s *Service) List(ctx context.Context, app string) ([]RegistryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	wanted := func(string) bool { return true }
	if app != "" {
		name := canonicalApp(app)
		if name == homebrewName {
			// Homebrew spreads over several keys, all of them prefixed.
			wanted = func(key string) bool { return strings.HasPrefix(key, homebrewName) }
		} else {
			key := sourceKey(name)
			wanted = func(k string) bool { return k == key }
		}
	}

	entries := make([]RegistryEntry, 0)
	for region, regionSources := range *s.sources {
		for name, urls := range regionSources {
			if !wanted(name) {
				continue
			}
			for _, url := range urls {
				entries = append(entries, RegistryEntry{App: name, Region: string(region), URL: url})
			}
		}
	}

	if app != "" && len(entries) == 0 {
		return nil, fmt.Errorf("unknown app %q: no registry sources are known for it", app)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].App != entries[j].App {
			return entries[i].App < entries[j].App
		}
		if entries[i].Region != entries[j].Region {
			return entries[i].Region < entries[j].Region
		}
		return entries[i].URL < entries[j].URL
	})
	return entries, nil
}

// Use points the given apps at the given region's mirrors. An empty apps slice
// means every app that is installed.
//
// An unknown region or an unknown app fails before anything is planned, and is
// the only case where the result is nil. Otherwise every selected app gets a
// ChangeResult, a failure on one of them is recorded there and the returned
// error joins them all, so a broken backend never stops the others.
//
// With dryRun nothing is written and no snapshot is taken. A real run that has
// at least one change to make copies every file it is about to touch into a
// single snapshot first, whose ID is returned in UseResult.SnapshotID.
func (s *Service) Use(ctx context.Context, region string, apps []string, dryRun bool) (*UseResult, error) {
	regionValue, ok := structs.StringToRegion(region)
	if !ok {
		return nil, fmt.Errorf("unknown region %q: supported regions are %s", region, strings.Join(structs.AllRegionStrings(), ", "))
	}
	regionSources := (*s.sources)[regionValue]

	targets, err := s.resolveApps(apps)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &UseResult{Changes: make([]ChangeResult, 0, len(targets)), DryRun: dryRun}

	// pending is a change that is not a no-op, kept with the index of the
	// result it belongs to so a failure can be reported against the right app.
	type pending struct {
		index  int
		change change
	}
	var (
		planned []pending
		paths   []string
	)

	for _, sw := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		outcome := ChangeResult{App: sw.Name()}
		urls := regionSources[sourceKey(sw.Name())]
		if len(urls) == 0 {
			outcome.Err = fmt.Errorf("region %s ships no %s mirror", region, sw.Name())
			result.Changes = append(result.Changes, outcome)
			continue
		}

		next, err := sw.Plan(urls[0], regionSources)
		if err != nil {
			outcome.To = urls[0]
			outcome.Err = fmt.Errorf("failed to plan the %s change: %w", sw.Name(), err)
			result.Changes = append(result.Changes, outcome)
			continue
		}

		outcome.From, outcome.To = next.From(), next.To()
		outcome.Noop = next.IsNoop()
		outcome.Diff = next.Diff()
		if files := next.Paths(); len(files) > 0 {
			outcome.Path = files[0]
		}
		result.Changes = append(result.Changes, outcome)

		if !outcome.Noop {
			planned = append(planned, pending{index: len(result.Changes) - 1, change: next})
			paths = append(paths, next.Paths()...)
		}
	}

	if dryRun || len(planned) == 0 {
		return result, result.Err()
	}

	if s.history == nil {
		return result, errors.New("refusing to change anything: no snapshot history is configured")
	}
	snapshot, err := s.history.Save(ctx, "use "+region, paths)
	if err != nil {
		return result, fmt.Errorf("failed to snapshot the configuration before changing it: %w", err)
	}
	result.SnapshotID = snapshot.ID

	for _, p := range planned {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := p.change.Apply(); err != nil {
			result.Changes[p.index].Err = fmt.Errorf("failed to point %s at the %s mirror: %w", result.Changes[p.index].App, region, err)
		}
	}
	return result, result.Err()
}

// History returns every snapshot taken so far, newest first.
func (s *Service) History(ctx context.Context) ([]history.Snapshot, error) {
	if s.history == nil {
		return nil, errors.New("no snapshot history is configured")
	}
	return s.history.List(ctx)
}

// Undo puts back the files captured in a snapshot. An empty id means the
// newest one. The restore itself is snapshotted first, so an undo can be
// undone.
func (s *Service) Undo(ctx context.Context, id string) (*history.Snapshot, error) {
	if s.history == nil {
		return nil, errors.New("no snapshot history is configured")
	}

	if id == "" {
		snapshots, err := s.history.List(ctx)
		if err != nil {
			return nil, err
		}
		if len(snapshots) == 0 {
			return nil, errors.New("there is nothing to undo: regtool has not changed anything yet")
		}
		id = snapshots[0].ID
	}

	snapshot, err := s.history.Get(id)
	if err != nil {
		return nil, err
	}
	if err := s.history.Restore(ctx, snapshot.ID); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Refresh records the registry every installed app currently points at, so a
// later change can be compared against it.
func (s *Service) Refresh(ctx context.Context) error {
	installed, err := s.detected()
	if err != nil {
		return err
	}

	current := make(map[string]string, len(installed))
	var errs []error
	for _, sw := range installed {
		if err := ctx.Err(); err != nil {
			return err
		}

		url, err := sw.Current()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read the current %s registry: %w", sw.Name(), err))
			continue
		}
		current[sw.Name()] = url
	}

	if err := localdata.SaveToBackup(current); err != nil {
		errs = append(errs, fmt.Errorf("failed to save the registry backup: %w", err))
	}
	return errors.Join(errs...)
}

// Apps returns the name of every backend this build knows about, whether or not
// it is installed.
func (s *Service) Apps() []string {
	names := make([]string, 0, len(s.switchers))
	for _, sw := range s.switchers {
		names = append(names, sw.Name())
	}
	return names
}

// detected returns the switchers whose tool appears to be present, in the order
// they were registered.
func (s *Service) detected() ([]switcher, error) {
	out := make([]switcher, 0, len(s.switchers))
	var errs []error
	for _, sw := range s.switchers {
		installed, err := sw.Detect()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to detect %s: %w", sw.Name(), err))
			continue
		}
		if installed {
			out = append(out, sw)
		}
	}
	return out, errors.Join(errs...)
}

// resolveApps maps the user supplied app names onto switchers, dropping
// duplicates and keeping the order the user asked for. An app that is named
// explicitly is used whether or not it was detected: asking for it is reason
// enough to write its configuration file.
func (s *Service) resolveApps(apps []string) ([]switcher, error) {
	if len(apps) == 0 {
		return s.detected()
	}

	known := make(map[string]switcher, len(s.switchers))
	for _, sw := range s.switchers {
		known[sw.Name()] = sw
	}

	out := make([]switcher, 0, len(apps))
	seen := make(map[string]bool, len(apps))
	for _, app := range apps {
		name := canonicalApp(app)
		sw, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown app %q: supported apps are %s", app, strings.Join(s.Apps(), ", "))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, sw)
	}
	return out, nil
}

// matchRegion finds the region whose mirror for the given sources key equals
// url.
func (s *Service) matchRegion(key, url string) (string, bool) {
	normalized := normalizeURL(url)
	if normalized == "" {
		return "", false
	}
	for _, region := range structs.AllRegions() {
		for _, candidate := range (*s.sources)[region][key] {
			if normalizeURL(candidate) == normalized {
				return string(region), true
			}
		}
	}
	return "", false
}

// normalizeURL makes registry URLs comparable: package managers happily report
// the same mirror with or without a trailing slash.
func normalizeURL(url string) string {
	return strings.TrimRight(strings.TrimSpace(url), "/")
}
