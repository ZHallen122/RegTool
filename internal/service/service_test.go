package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/localdata"
	"github.com/ZHallen122/RegTool/source/structs"
)

// fakeManager is an AppManager that lives entirely in memory so the tests never
// need npm, pip, gem or brew to be installed.
type fakeManager struct {
	name     string
	current  string
	getErr   error
	setErr   error
	setCalls int
	setRegns []structs.Region
}

func (f *fakeManager) GetCurrRegistry() (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.current, nil
}

func (f *fakeManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	f.setCalls++
	f.setRegns = append(f.setRegns, region)
	if f.setErr != nil {
		return "", f.setErr
	}
	urls := (*sources)[region][f.name]
	if len(urls) == 0 {
		return "", errors.New("no sources for region")
	}
	f.current = urls[0]
	return f.current, nil
}

func (f *fakeManager) IsExists() bool { return true }

func testSources() *structs.RegistrySources {
	return &structs.RegistrySources{
		structs.US: {
			"npm": {"https://registry.npmjs.org"},
			"pip": {"https://pypi.org/simple"},
		},
		structs.CN: {
			"npm": {"https://registry.npmmirror.com"},
			"pip": {"https://pypi.tuna.tsinghua.edu.cn/simple"},
		},
		structs.EU: {
			"npm": {"https://registry.npmjs.org"},
		},
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name     string
		managers map[string]source.AppManager
		want     []AppStatus
	}{
		{
			name: "known mirror is matched to its region",
			managers: map[string]source.AppManager{
				"npm": &fakeManager{name: "npm", current: "https://registry.npmmirror.com"},
			},
			want: []AppStatus{{App: "npm", Region: "cn", URL: "https://registry.npmmirror.com"}},
		},
		{
			name: "a trailing slash still matches",
			managers: map[string]source.AppManager{
				"npm": &fakeManager{name: "npm", current: "https://registry.npmjs.org/"},
			},
			want: []AppStatus{{App: "npm", Region: "us", URL: "https://registry.npmjs.org/"}},
		},
		{
			name: "an unknown mirror is reported as local",
			managers: map[string]source.AppManager{
				"npm": &fakeManager{name: "npm", current: "https://npm.example.com"},
			},
			want: []AppStatus{{App: "npm", Region: LocalRegion, URL: "https://npm.example.com"}},
		},
		{
			name: "apps are reported in a stable order",
			managers: map[string]source.AppManager{
				"pip": &fakeManager{name: "pip", current: "https://pypi.org/simple"},
				"npm": &fakeManager{name: "npm", current: "https://registry.npmjs.org"},
			},
			want: []AppStatus{
				{App: "npm", Region: "us", URL: "https://registry.npmjs.org"},
				{App: "pip", Region: "us", URL: "https://pypi.org/simple"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(testSources(), tt.managers).Status(context.Background())
			if err != nil {
				t.Fatalf("Status() returned an unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Status() returned %d statuses, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, want := range tt.want {
				if got[i].App != want.App || got[i].Region != want.Region || got[i].URL != want.URL {
					t.Errorf("Status()[%d] = %+v, want %+v", i, got[i], want)
				}
				if got[i].Err != nil {
					t.Errorf("Status()[%d] carries an unexpected error: %v", i, got[i].Err)
				}
			}
		})
	}
}

func TestStatusReportsBackendFailureWithoutHidingTheOthers(t *testing.T) {
	svc := New(testSources(), map[string]source.AppManager{
		"npm": &fakeManager{name: "npm", getErr: errors.New("npm is broken")},
		"pip": &fakeManager{name: "pip", current: "https://pypi.org/simple"},
	})

	got, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() returned an unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Status() returned %d statuses, want 2: %+v", len(got), got)
	}
	if got[0].Err == nil {
		t.Error("Status() did not report the npm failure")
	}
	if got[1].Err != nil || got[1].Region != "us" {
		t.Errorf("Status() lost the healthy pip entry: %+v", got[1])
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name      string
		app       string
		wantCount int
		wantFirst RegistryEntry
		wantErr   bool
	}{
		{
			name:      "no app lists every mirror",
			app:       "",
			wantCount: 5,
			wantFirst: RegistryEntry{App: "npm", Region: "cn", URL: "https://registry.npmmirror.com"},
		},
		{
			name:      "a single app lists only its mirrors",
			app:       "pip",
			wantCount: 2,
			wantFirst: RegistryEntry{App: "pip", Region: "cn", URL: "https://pypi.tuna.tsinghua.edu.cn/simple"},
		},
		{
			name:    "an unknown app is an error",
			app:     "cargo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(testSources(), nil).List(context.Background(), tt.app)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("List(%q) succeeded, want an error", tt.app)
				}
				return
			}
			if err != nil {
				t.Fatalf("List(%q) returned an unexpected error: %v", tt.app, err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("List(%q) returned %d entries, want %d: %+v", tt.app, len(got), tt.wantCount, got)
			}
			if got[0] != tt.wantFirst {
				t.Errorf("List(%q)[0] = %+v, want %+v", tt.app, got[0], tt.wantFirst)
			}
		})
	}
}

func TestUse(t *testing.T) {
	tests := []struct {
		name        string
		region      string
		apps        []string
		dryRun      bool
		setErr      error
		wantErr     bool
		wantResults []ChangeResult
		wantSetFor  map[string]int
	}{
		{
			name:   "a single app is switched",
			region: "cn",
			apps:   []string{"npm"},
			wantResults: []ChangeResult{
				{App: "npm", From: "https://registry.npmjs.org", To: "https://registry.npmmirror.com"},
			},
			wantSetFor: map[string]int{"npm": 1, "pip": 0},
		},
		{
			name:   "no app means every installed app",
			region: "cn",
			apps:   nil,
			wantResults: []ChangeResult{
				{App: "npm", From: "https://registry.npmjs.org", To: "https://registry.npmmirror.com"},
				{App: "pip", From: "https://pypi.org/simple", To: "https://pypi.tuna.tsinghua.edu.cn/simple"},
			},
			wantSetFor: map[string]int{"npm": 1, "pip": 1},
		},
		{
			name:   "a dry run reports the change without applying it",
			region: "cn",
			apps:   []string{"npm"},
			dryRun: true,
			wantResults: []ChangeResult{
				{App: "npm", From: "https://registry.npmjs.org", To: "https://registry.npmmirror.com"},
			},
			wantSetFor: map[string]int{"npm": 0, "pip": 0},
		},
		{
			name:    "an unknown region is an error",
			region:  "xx",
			apps:    []string{"npm"},
			wantErr: true,
		},
		{
			name:    "an unknown app is an error",
			region:  "cn",
			apps:    []string{"cargo"},
			wantErr: true,
		},
		{
			name:   "a duplicated app is only changed once",
			region: "cn",
			apps:   []string{"npm", "npm"},
			wantResults: []ChangeResult{
				{App: "npm", From: "https://registry.npmjs.org", To: "https://registry.npmmirror.com"},
			},
			wantSetFor: map[string]int{"npm": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			npm := &fakeManager{name: "npm", current: "https://registry.npmjs.org", setErr: tt.setErr}
			pip := &fakeManager{name: "pip", current: "https://pypi.org/simple"}
			svc := New(testSources(), map[string]source.AppManager{"npm": npm, "pip": pip})

			got, err := svc.Use(context.Background(), tt.region, tt.apps, tt.dryRun)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Use(%q, %v) succeeded, want an error", tt.region, tt.apps)
				}
				if npm.setCalls != 0 || pip.setCalls != 0 {
					t.Errorf("Use() changed a registry even though it failed validation")
				}
				return
			}
			if err != nil {
				t.Fatalf("Use(%q, %v) returned an unexpected error: %v", tt.region, tt.apps, err)
			}

			if len(got) != len(tt.wantResults) {
				t.Fatalf("Use() returned %d results, want %d: %+v", len(got), len(tt.wantResults), got)
			}
			for i, want := range tt.wantResults {
				if got[i].App != want.App || got[i].From != want.From || got[i].To != want.To {
					t.Errorf("Use()[%d] = %+v, want %+v", i, got[i], want)
				}
				if got[i].Err != nil {
					t.Errorf("Use()[%d] carries an unexpected error: %v", i, got[i].Err)
				}
			}

			managers := map[string]*fakeManager{"npm": npm, "pip": pip}
			for name, wantCalls := range tt.wantSetFor {
				if managers[name].setCalls != wantCalls {
					t.Errorf("SetRegistry was called %d times on %s, want %d", managers[name].setCalls, name, wantCalls)
				}
			}
		})
	}
}

func TestUseDryRunLeavesBackendsUntouched(t *testing.T) {
	npm := &fakeManager{name: "npm", current: "https://registry.npmjs.org"}
	svc := New(testSources(), map[string]source.AppManager{"npm": npm})

	if _, err := svc.Use(context.Background(), "cn", nil, true); err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if npm.setCalls != 0 {
		t.Errorf("a dry run called SetRegistry %d times, want 0", npm.setCalls)
	}
	if npm.current != "https://registry.npmjs.org" {
		t.Errorf("a dry run changed the registry to %q", npm.current)
	}
}

func TestUseReportsPartialFailure(t *testing.T) {
	npm := &fakeManager{name: "npm", current: "https://registry.npmjs.org", setErr: errors.New("npm refused")}
	pip := &fakeManager{name: "pip", current: "https://pypi.org/simple"}
	svc := New(testSources(), map[string]source.AppManager{"npm": npm, "pip": pip})

	got, err := svc.Use(context.Background(), "cn", nil, false)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Use() returned %d results, want 2: %+v", len(got), got)
	}
	if got[0].Err == nil {
		t.Error("Use() did not report the npm failure")
	}
	if got[1].Err != nil {
		t.Errorf("Use() reported an error for pip: %v", got[1].Err)
	}
	if pip.current != "https://pypi.tuna.tsinghua.edu.cn/simple" {
		t.Errorf("pip was left at %q, want the cn mirror", pip.current)
	}
}

func TestUseRespectsCancelledContext(t *testing.T) {
	npm := &fakeManager{name: "npm", current: "https://registry.npmjs.org"}
	svc := New(testSources(), map[string]source.AppManager{"npm": npm})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.Use(ctx, "cn", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Use() returned %v, want context.Canceled", err)
	}
	if npm.setCalls != 0 {
		t.Errorf("Use() changed a registry despite the cancelled context")
	}
}

func TestRefreshRecordsCurrentRegistries(t *testing.T) {
	backup := redirectBackupFile(t)

	npm := &fakeManager{name: "npm", current: "https://registry.npmmirror.com"}
	svc := New(testSources(), map[string]source.AppManager{"npm": npm})

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() returned an unexpected error: %v", err)
	}

	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("failed to read the backup file: %v", err)
	}
	var recorded map[string]string
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("failed to decode the backup file: %v", err)
	}
	if recorded["npm"] != "https://registry.npmmirror.com" {
		t.Errorf("the backup recorded %q for npm, want the cn mirror", recorded["npm"])
	}
}

func TestRefreshReportsBackendFailure(t *testing.T) {
	redirectBackupFile(t)

	svc := New(testSources(), map[string]source.AppManager{
		"npm": &fakeManager{name: "npm", getErr: errors.New("npm is broken")},
	})

	if err := svc.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() succeeded, want an error")
	}
}

func TestChangeResultMarshalsItsError(t *testing.T) {
	raw, err := json.Marshal(ChangeResult{App: "npm", Err: errors.New("boom")})
	if err != nil {
		t.Fatalf("failed to marshal a ChangeResult: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to decode the marshalled ChangeResult: %v", err)
	}
	if decoded["error"] != "boom" {
		t.Errorf("the marshalled ChangeResult carries error %q, want %q", decoded["error"], "boom")
	}
}

// redirectBackupFile points the localdata package at a temporary directory so
// the tests never write to the developer's home directory.
func redirectBackupFile(t *testing.T) string {
	t.Helper()

	oldConfig, oldHub, oldFile := localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE
	t.Cleanup(func() {
		localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE = oldConfig, oldHub, oldFile
	})

	root := t.TempDir()
	localdata.DOT_CONFIG_DIR = filepath.Join(root, localdata.DOT_CONFIG_NAME)
	localdata.REGISTRY_HUB_DIR = filepath.Join(localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_FOLDER_NAME)
	localdata.SOURCE_BACKUP_FILE = filepath.Join(localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE_NAME)
	return localdata.SOURCE_BACKUP_FILE
}
