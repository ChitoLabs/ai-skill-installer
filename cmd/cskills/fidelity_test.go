package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// buildFidelityFixture creates a source tree exercising every fidelity axis
// this test suite compares against Bash's own "cp -a" (install-skills.sh:758,
// 804, 809, 814): an executable file, a plain file, a symlink inside the
// tree, a symlink escaping the tree, and a hard-linked pair.
func buildFidelityFixture(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin-tool"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "notes.txt"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("nested", "notes.txt"), filepath.Join(source, "relative-link")); err != nil {
		t.Fatal(err)
	}
	// Neither Bash's validate_snapshot nor Go's ValidateSnapshot reject an
	// escaping symlink inside an already-captured target tree; both only
	// preserve the link text verbatim, matching cp -a -d.
	if err := os.Symlink("/nonexistent/outside/target", filepath.Join(source, "escaping-link")); err != nil {
		t.Fatal(err)
	}
	hardlinkA := filepath.Join(source, "hardlink-a")
	hardlinkB := filepath.Join(source, "nested", "hardlink-b")
	if err := os.WriteFile(hardlinkA, []byte("shared"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(hardlinkA, hardlinkB); err != nil {
		t.Fatal(err)
	}
	return source
}

func runCpArchive(t *testing.T, source, destination string) {
	t.Helper()
	if _, err := exec.LookPath("cp"); err != nil {
		t.Skipf("cp is unavailable: %v", err)
	}
	output, err := exec.Command("cp", "-a", "--", source, destination).CombinedOutput()
	if err != nil {
		t.Fatalf("cp -a failed: %v\n%s", err, output)
	}
}

// TestCopyTreeMatchesCpArchiveModesAndSymlinks proves copyTree (used by both
// snapshot capture and restore staging) matches the file-mode and symlink
// fidelity of the same "cp -a" Bash uses, including a symlink that escapes
// the copied tree.
func TestCopyTreeMatchesCpArchiveModesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	source := buildFidelityFixture(t, root)

	destGo := filepath.Join(root, "dest-go")
	if err := copyTree(destGo, source); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}
	destBash := filepath.Join(root, "dest-bash")
	runCpArchive(t, source, destBash)

	for _, relative := range []string{"bin-tool", filepath.Join("nested", "notes.txt")} {
		wantInfo, err := os.Lstat(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, dest := range []string{destGo, destBash} {
			gotInfo, err := os.Lstat(filepath.Join(dest, relative))
			if err != nil {
				t.Fatalf("stat %s in %s: %v", relative, dest, err)
			}
			if gotInfo.Mode().Perm() != wantInfo.Mode().Perm() {
				t.Errorf("%s mode in %s = %v, want %v", relative, dest, gotInfo.Mode().Perm(), wantInfo.Mode().Perm())
			}
		}
	}

	for _, relative := range []string{"relative-link", "escaping-link"} {
		want, err := os.Readlink(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, dest := range []string{destGo, destBash} {
			got, err := os.Readlink(filepath.Join(dest, relative))
			if err != nil || got != want {
				t.Errorf("%s symlink in %s = %q, %v; want %q", relative, dest, got, err, want)
			}
		}
	}
}

// TestCopyTreePreservesHardLinksLikeCpArchive proves the hard-link fix in
// copyTree: files sharing an inode in the source tree still share one after
// being copied, matching cp -a's --preserve=links (implied by --archive).
func TestCopyTreePreservesHardLinksLikeCpArchive(t *testing.T) {
	root := t.TempDir()
	source := buildFidelityFixture(t, root)

	destGo := filepath.Join(root, "dest-go")
	if err := copyTree(destGo, source); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}
	destBash := filepath.Join(root, "dest-bash")
	runCpArchive(t, source, destBash)

	sourceA, err := os.Stat(filepath.Join(source, "hardlink-a"))
	if err != nil {
		t.Fatal(err)
	}
	sourceB, err := os.Stat(filepath.Join(source, "nested", "hardlink-b"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceA, sourceB) {
		t.Fatal("fixture setup did not actually hard-link the source files")
	}

	for name, dest := range map[string]string{"Go copyTree": destGo, "Bash cp -a": destBash} {
		infoA, err := os.Stat(filepath.Join(dest, "hardlink-a"))
		if err != nil {
			t.Fatal(err)
		}
		infoB, err := os.Stat(filepath.Join(dest, "nested", "hardlink-b"))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(infoA, infoB) {
			t.Errorf("%s did not preserve the hard-link relationship", name)
		}
		if content, err := os.ReadFile(filepath.Join(dest, "hardlink-a")); err != nil || string(content) != "shared" {
			t.Errorf("%s hard-linked content = %q, %v", name, content, err)
		}
	}
}

// TestCopyTreeDoesNotPreserveExtendedAttributesLikeCpArchive documents a
// known, unfixed fidelity limitation: cp -a (--preserve=all) preserves user
// extended attributes, but copyTree has no xattr support. This is reported
// rather than silently patched, since xattr preservation touches error
// handling for unsupported/forbidden namespaces beyond this task's bounded
// scope. Skips when the filesystem backing t.TempDir() does not support user
// extended attributes (for example, some tmpfs mounts).
func TestCopyTreeDoesNotPreserveExtendedAttributesLikeCpArchive(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(source, "tagged.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o640); err != nil {
		t.Fatal(err)
	}

	const attrName = "user.cskill.fidelity-test"
	if err := syscall.Setxattr(filePath, attrName, []byte("value"), 0); err != nil {
		t.Skipf("filesystem does not support user extended attributes: %v", err)
	}

	destGo := filepath.Join(root, "dest-go")
	if err := copyTree(destGo, source); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}
	destBash := filepath.Join(root, "dest-bash")
	runCpArchive(t, source, destBash)

	buffer := make([]byte, 64)
	if _, err := syscall.Getxattr(filepath.Join(destBash, "tagged.txt"), attrName, buffer); err != nil {
		t.Fatalf("cp -a did not preserve the extended attribute as expected: %v", err)
	}
	if _, err := syscall.Getxattr(filepath.Join(destGo, "tagged.txt"), attrName, buffer); err == nil {
		t.Fatal("copyTree unexpectedly preserved an extended attribute; update the documented fidelity limitation if this is now intentional")
	}
}
