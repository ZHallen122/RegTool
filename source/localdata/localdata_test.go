package localdata

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// useTempPaths points the package-level path variables at a per-test temporary
// directory and restores them afterwards. The package reads these vars at call
// time, so overriding them is enough to isolate the tests from $HOME.
func useTempPaths(t *testing.T) {
	t.Helper()

	oldConfig, oldHub, oldFile := DOT_CONFIG_DIR, REGISTRY_HUB_DIR, SOURCE_BACKUP_FILE
	t.Cleanup(func() {
		DOT_CONFIG_DIR, REGISTRY_HUB_DIR, SOURCE_BACKUP_FILE = oldConfig, oldHub, oldFile
	})

	root := t.TempDir()
	DOT_CONFIG_DIR = filepath.Join(root, DOT_CONFIG_NAME)
	REGISTRY_HUB_DIR = filepath.Join(DOT_CONFIG_DIR, REGISTRY_HUB_FOLDER_NAME)
	SOURCE_BACKUP_FILE = filepath.Join(REGISTRY_HUB_DIR, SOURCE_BACKUP_FILE_NAME)
}

// writeBackup writes raw bytes to the backup file, creating its directory.
func writeBackup(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(REGISTRY_HUB_DIR, READ_PERMISSION); err != nil {
		t.Fatalf("failed to create hub dir: %v", err)
	}
	if err := os.WriteFile(SOURCE_BACKUP_FILE, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write backup file: %v", err)
	}
}

func TestReadBackupFileMissing(t *testing.T) {
	useTempPaths(t)

	data, err := ReadBackupFile()
	if err != nil {
		t.Fatalf("ReadBackupFile() error = %v, want nil", err)
	}
	if data == nil {
		t.Fatal("ReadBackupFile() returned a nil map, want an empty non-nil map")
	}
	if len(data) != 0 {
		t.Fatalf("ReadBackupFile() = %v, want empty map", data)
	}

	// The read must not create anything on disk.
	if _, statErr := os.Stat(SOURCE_BACKUP_FILE); !os.IsNotExist(statErr) {
		t.Fatalf("backup file exists after read: %v", statErr)
	}
}

func TestReadBackupFileExisting(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name:    "empty json object",
			content: `{}`,
			want:    map[string]string{},
		},
		{
			name:    "single entry",
			content: `{"npm":"https://registry.npmjs.org/"}`,
			want:    map[string]string{"npm": "https://registry.npmjs.org/"},
		},
		{
			name:    "several entries",
			content: `{"npm":"https://registry.npmjs.org/","yarn":"https://registry.yarnpkg.com/","homebrew_api_domain":"https://formulae.brew.sh/api"}`,
			want: map[string]string{
				"npm":                 "https://registry.npmjs.org/",
				"yarn":                "https://registry.yarnpkg.com/",
				"homebrew_api_domain": "https://formulae.brew.sh/api",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			useTempPaths(t)
			writeBackup(t, tt.content)

			got, err := ReadBackupFile()
			if err != nil {
				t.Fatalf("ReadBackupFile() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ReadBackupFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadBackupFileInvalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "not json", content: "not json at all"},
		{name: "truncated object", content: `{"npm":`},
		{name: "wrong value type", content: `{"npm":["https://registry.npmjs.org/"]}`},
		{name: "top level array", content: `["npm"]`},
		{name: "empty file", content: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			useTempPaths(t)
			writeBackup(t, tt.content)

			got, err := ReadBackupFile()
			if err == nil {
				t.Fatalf("ReadBackupFile() = %v, nil error; want an error", got)
			}
			if got != nil {
				t.Fatalf("ReadBackupFile() = %v on error, want nil map", got)
			}
		})
	}
}

func TestSaveToBackupRoundTrip(t *testing.T) {
	useTempPaths(t)

	want := map[string]string{
		"npm":                 "https://registry.npmmirror.com/",
		"yarn":                "https://registry.npmmirror.com/",
		"homebrew_api_domain": "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api",
	}

	if err := SaveToBackup(want); err != nil {
		t.Fatalf("SaveToBackup() error = %v, want nil", err)
	}

	// SaveToBackup is responsible for creating the directory tree.
	if _, err := os.Stat(REGISTRY_HUB_DIR); err != nil {
		t.Fatalf("registry hub dir was not created: %v", err)
	}

	got, err := ReadBackupFile()
	if err != nil {
		t.Fatalf("ReadBackupFile() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %v, want %v", got, want)
	}
}

func TestSaveToBackupMerges(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		save     map[string]string
		want     map[string]string
	}{
		{
			name:     "adds new keys to existing ones",
			existing: map[string]string{"npm": "https://registry.npmjs.org/"},
			save:     map[string]string{"yarn": "https://registry.yarnpkg.com/"},
			want: map[string]string{
				"npm":  "https://registry.npmjs.org/",
				"yarn": "https://registry.yarnpkg.com/",
			},
		},
		{
			name:     "overwrites a key that is saved again",
			existing: map[string]string{"npm": "https://registry.npmjs.org/"},
			save:     map[string]string{"npm": "https://registry.npmmirror.com/"},
			want:     map[string]string{"npm": "https://registry.npmmirror.com/"},
		},
		{
			name:     "empty save leaves existing data untouched",
			existing: map[string]string{"npm": "https://registry.npmjs.org/"},
			save:     map[string]string{},
			want:     map[string]string{"npm": "https://registry.npmjs.org/"},
		},
		{
			name:     "nil save leaves existing data untouched",
			existing: map[string]string{"gem": "https://rubygems.org/"},
			save:     nil,
			want:     map[string]string{"gem": "https://rubygems.org/"},
		},
		{
			name:     "mixed add and overwrite",
			existing: map[string]string{"npm": "https://registry.npmjs.org/", "gem": "https://rubygems.org/"},
			save:     map[string]string{"npm": "https://registry.npmmirror.com/", "pip": "https://pypi.org/simple"},
			want: map[string]string{
				"npm": "https://registry.npmmirror.com/",
				"gem": "https://rubygems.org/",
				"pip": "https://pypi.org/simple",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			useTempPaths(t)

			if err := SaveToBackup(tt.existing); err != nil {
				t.Fatalf("seeding SaveToBackup() error = %v", err)
			}
			if err := SaveToBackup(tt.save); err != nil {
				t.Fatalf("SaveToBackup() error = %v, want nil", err)
			}

			got, err := ReadBackupFile()
			if err != nil {
				t.Fatalf("ReadBackupFile() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("after merge = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSaveToBackupFailsOnInvalidExistingFile(t *testing.T) {
	useTempPaths(t)
	writeBackup(t, "not json")

	if err := SaveToBackup(map[string]string{"npm": "https://registry.npmjs.org/"}); err == nil {
		t.Fatal("SaveToBackup() = nil error, want an error when the existing file is corrupt")
	}
}
