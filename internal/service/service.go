// Package service holds the registry operations shared by the CLI and the
// TUI. Everything here returns plain data structures: nothing in this package
// prints, exits, or reads from the terminal, so both front ends can render the
// results however they like and tests can drive it with fake backends.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/localdata"
	"github.com/ZHallen122/RegTool/source/structs"
)

// LocalRegion is the region reported for a registry URL that does not match
// any of the known mirrors.
const LocalRegion = "local"

// AppStatus describes the registry a single installed app currently points at.
type AppStatus struct {
	// App is the primary name of the package manager.
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
	// App is the primary name of the package manager.
	App string `json:"app"`
	// From is the registry the app pointed at before the change, empty when it
	// could not be read.
	From string `json:"from"`
	// To is the registry the app now points at, or would point at for a dry
	// run. It is empty when the region ships no mirror for this app.
	To string `json:"to"`
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

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Service runs registry operations against a fixed set of mirrors and a fixed
// set of package manager backends. Both are injected so tests can supply fakes.
type Service struct {
	sources  *structs.RegistrySources
	managers map[string]source.AppManager
}

// New builds a Service from an already loaded set of mirrors and the backends
// it should operate on. The managers map is keyed by the primary app name.
func New(sources *structs.RegistrySources, managers map[string]source.AppManager) *Service {
	if sources == nil {
		sources = &structs.RegistrySources{}
	}
	if managers == nil {
		managers = map[string]source.AppManager{}
	}
	return &Service{sources: sources, managers: managers}
}

// Load builds a Service from the mirrors reachable in ctx and from every
// package manager that is actually installed on this machine.
func Load(ctx context.Context) (*Service, error) {
	sources, err := source.LoadRegistrySources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load registry sources: %w", err)
	}
	return New(sources, source.GetAllRegisteredApp()), nil
}

// Status reports, for every installed app, the registry it currently points at
// and the region that registry belongs to. Per-app failures are carried in
// AppStatus.Err; the returned error is reserved for a cancelled context.
func (s *Service) Status(ctx context.Context) ([]AppStatus, error) {
	names := s.appNames()
	out := make([]AppStatus, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		status := AppStatus{App: name, Region: LocalRegion}
		current, err := s.managers[name].GetCurrRegistry()
		if err != nil {
			status.Err = fmt.Errorf("failed to read current %s registry: %w", name, err)
			out = append(out, status)
			continue
		}

		status.URL = current
		if region, ok := s.matchRegion(name, current); ok {
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

	want := ""
	if app != "" {
		want = s.resolveSourceKey(app)
	}

	entries := make([]RegistryEntry, 0)
	for region, regionSources := range *s.sources {
		for name, urls := range regionSources {
			if want != "" && name != want {
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
// means every installed app. With dryRun the backends are only queried, never
// written to.
//
// An unknown region or an unknown app is returned as an error and nothing is
// changed. A backend that fails mid-run is reported in that app's
// ChangeResult.Err so the remaining apps still get their chance.
func (s *Service) Use(ctx context.Context, region string, apps []string, dryRun bool) ([]ChangeResult, error) {
	regionValue, ok := structs.StringToRegion(region)
	if !ok {
		return nil, fmt.Errorf("unknown region %q: supported regions are %s", region, strings.Join(structs.AllRegionStrings(), ", "))
	}

	targets, err := s.resolveApps(apps)
	if err != nil {
		return nil, err
	}

	results := make([]ChangeResult, 0, len(targets))
	for _, name := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		manager := s.managers[name]
		result := ChangeResult{App: name}
		if current, err := manager.GetCurrRegistry(); err == nil {
			result.From = current
		}

		if dryRun {
			// Backends whose mirrors are spread over several keys (homebrew)
			// have no single entry to look up, so To stays empty for them.
			result.To, _ = s.registryURL(regionValue, name)
			results = append(results, result)
			continue
		}

		target, err := manager.SetRegistry(regionValue, s.sources)
		if err != nil {
			result.Err = fmt.Errorf("failed to set %s registry to region %s: %w", name, region, err)
		}
		result.To = target
		results = append(results, result)
	}
	return results, nil
}

// Refresh records the registry every installed app currently points at, so a
// later change can be compared against it.
func (s *Service) Refresh(ctx context.Context) error {
	current := make(map[string]string)
	var errs []error
	for _, name := range s.appNames() {
		if err := ctx.Err(); err != nil {
			return err
		}

		url, err := s.managers[name].GetCurrRegistry()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read current %s registry: %w", name, err))
			continue
		}
		current[name] = url
	}

	if err := localdata.SaveToBackup(current); err != nil {
		errs = append(errs, fmt.Errorf("failed to save registry backup: %w", err))
	}
	return errors.Join(errs...)
}

// appNames returns the managed app names in a stable order.
func (s *Service) appNames() []string {
	names := make([]string, 0, len(s.managers))
	for name := range s.managers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveApps maps the user supplied app names onto managed backends,
// dropping duplicates and keeping the order the user asked for.
func (s *Service) resolveApps(apps []string) ([]string, error) {
	if len(apps) == 0 {
		return s.appNames(), nil
	}

	out := make([]string, 0, len(apps))
	seen := make(map[string]bool, len(apps))
	for _, app := range apps {
		name := app
		if _, ok := s.managers[name]; !ok {
			name = alias.GetPrimary(app)
		}
		if _, ok := s.managers[name]; !ok {
			return nil, fmt.Errorf("unknown app %q: it is not installed or not supported", app)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// resolveSourceKey maps an app name onto the key used in the sources file,
// falling back to the alias table when the raw name is not a key.
func (s *Service) resolveSourceKey(app string) string {
	for _, regionSources := range *s.sources {
		if _, ok := regionSources[app]; ok {
			return app
		}
	}
	return alias.GetPrimary(app)
}

// registryURL returns the mirror a region ships for an app.
func (s *Service) registryURL(region structs.Region, app string) (string, bool) {
	urls := (*s.sources)[region][app]
	if len(urls) == 0 {
		return "", false
	}
	return urls[0], true
}

// matchRegion finds the region whose mirror for app equals url.
func (s *Service) matchRegion(app, url string) (string, bool) {
	normalized := normalizeURL(url)
	if normalized == "" {
		return "", false
	}
	for _, region := range structs.AllRegions() {
		for _, candidate := range (*s.sources)[region][app] {
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
