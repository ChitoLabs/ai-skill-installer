package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSkillTree(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, root string)
		minimum   int
		wantCount int
		wantErr   string
	}{
		{
			name: "valid direct manifests",
			prepare: func(t *testing.T, root string) {
				makeSkill(t, root, "alpha")
				makeSkill(t, root, "beta")
			},
			minimum: 2, wantCount: 2,
		},
		{
			name:    "minimum count is enforced",
			prepare: func(t *testing.T, root string) { makeSkill(t, root, "alpha") },
			minimum: 2, wantErr: "minimum is 2",
		},
		{
			name: "nested manifest is not a skill",
			prepare: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "collection", "nested"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeManifest(t, filepath.Join(root, "collection", "nested", "SKILL.md"))
			},
			minimum: 1, wantErr: "minimum is 1",
		},
		{
			name: "source root symlink",
			prepare: func(t *testing.T, root string) {
				real := root + "-real"
				if err := os.Mkdir(real, 0o755); err != nil {
					t.Fatal(err)
				}
				makeSkill(t, real, "alpha")
				if err := os.Symlink(real, root); err != nil {
					t.Fatal(err)
				}
			},
			minimum: 1, wantErr: "not found or unsafe",
		},
		{
			name: "internal symlink",
			prepare: func(t *testing.T, root string) {
				makeSkill(t, root, "alpha")
				if err := os.Symlink("SKILL.md", filepath.Join(root, "alpha", "link")); err != nil {
					t.Fatal(err)
				}
			},
			minimum: 1, wantErr: "symbolic link",
		},
		{
			name: "codegraph directory",
			prepare: func(t *testing.T, root string) {
				makeSkill(t, root, "alpha")
				if err := os.MkdirAll(filepath.Join(root, "alpha", "nested", ".codegraph"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			minimum: 1, wantErr: ".codegraph",
		},
		{
			name: "non-regular direct manifest",
			prepare: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "alpha", "SKILL.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			minimum: 1, wantErr: "not a regular non-symlink file",
		},
		{
			name: "manifest symlink",
			prepare: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeManifest(t, filepath.Join(root, "beta.md"))
				if err := os.Symlink("../beta.md", filepath.Join(root, "alpha", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			minimum: 1, wantErr: "symbolic link",
		},
		{
			name:    "expected manifest count mismatch",
			prepare: func(t *testing.T, root string) { makeSkill(t, root, "alpha") },
			minimum: 1, wantCount: 2, wantErr: "expected 2 manifests but found 1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "skills")
			if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, root)
			count, err := ValidateSkillTree(root, test.minimum, test.wantCount)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ValidateSkillTree() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSkillTree() error = %v", err)
			}
			if count != test.wantCount {
				t.Errorf("manifest count = %d, want %d", count, test.wantCount)
			}
		})
	}
}

func TestValidateSkillTreeRejectsMissingAndNonDirectoryRoots(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{name: "missing", setup: func(t *testing.T, _ string) {}},
		{name: "regular file", setup: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "skills")
			test.setup(t, path)
			if _, err := ValidateSkillTree(path, 1, 0); err == nil {
				t.Fatal("ValidateSkillTree() unexpectedly accepted invalid root")
			}
		})
	}
}

func TestValidateSkillTreeRejectsCodegraphRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".codegraph")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateSkillTree(root, 1, 0); err == nil || !strings.Contains(err.Error(), ".codegraph") {
		t.Fatalf("ValidateSkillTree() error = %v, want .codegraph rejection", err)
	}
}

func makeSkill(t *testing.T, root, name string) {
	t.Helper()
	writeManifest(t, filepath.Join(root, name, "SKILL.md"))
}

func writeManifest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Test skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
