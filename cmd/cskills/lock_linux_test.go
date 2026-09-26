package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerLockCreatesParentAndCanBeReleasedAndReacquired(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "nested", "installer.lock")
	lock, err := acquireInstallerLock(lockPath)
	if err != nil {
		t.Fatalf("acquireInstallerLock() error = %v", err)
	}
	if info, err := os.Stat(lockPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("lock file stat = (%v, %v), want regular file", info, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("release installer lock: %v", err)
	}

	reacquired, err := acquireInstallerLock(lockPath)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("release reacquired lock: %v", err)
	}
}

func TestInstallerLockRejectsUnsafeLockFiles(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		lockPath  string
		wantError string
	}{
		{name: "symlink", lockPath: filepath.Join(tempDir, "link"), wantError: "symbolic link"},
		{name: "directory", lockPath: filepath.Join(tempDir, "directory"), wantError: "regular file"},
	}
	if err := os.Symlink(target, tests[0].lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tests[1].lockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if lock, err := acquireInstallerLock(test.lockPath); err == nil {
				_ = lock.Close()
				t.Fatal("acquireInstallerLock() unexpectedly succeeded")
			} else if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("acquireInstallerLock() error = %v, want %q", err, test.wantError)
			}
		})
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "untouched" {
		t.Fatalf("symlink target = %q, %v; want unchanged contents", data, err)
	}
}

func TestInstallerLockInteropWithFlock(t *testing.T) {
	flockPath, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock command is required for Bash interoperability coverage")
	}
	pathEnv := os.Getenv("PATH")
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "state", "installer.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tempDir, "target")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("preserve lock file contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("Bash-compatible flock holder blocks Go without mutation", func(t *testing.T) {
		holder := exec.Command(flockPath, "--exclusive", "--nonblock", "--no-fork", lockPath, "sleep", "30")
		holder.Env = []string{"PATH=" + pathEnv, "HOME=" + tempDir}
		if err := holder.Start(); err != nil {
			t.Fatalf("start flock holder: %v", err)
		}
		holderRunning := true
		t.Cleanup(func() {
			if holderRunning {
				_ = holder.Process.Kill()
				_ = holder.Wait()
			}
		})
		waitForFlockHeld(t, flockPath, lockPath, tempDir, pathEnv)

		if lock, err := acquireInstallerLock(lockPath); err == nil {
			_ = lock.Close()
			t.Fatal("Go lock acquisition succeeded while flock held the lock")
		} else if !errors.Is(err, errInstallerLockContended) {
			t.Fatalf("acquireInstallerLock() error = %v, want contention", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "before" {
			t.Fatalf("target after contention = %q, %v; want unchanged", data, err)
		}
		if data, err := os.ReadFile(lockPath); err != nil || string(data) != "preserve lock file contents" {
			t.Fatalf("lock file after contention = %q, %v; want unchanged", data, err)
		}
		if err := holder.Process.Kill(); err != nil {
			t.Fatalf("stop flock holder: %v", err)
		}
		_ = holder.Wait()
		holderRunning = false
	})

	t.Run("Go lock holder blocks Bash-compatible flock", func(t *testing.T) {
		lock, err := acquireInstallerLock(lockPath)
		if err != nil {
			t.Fatalf("acquireInstallerLock() error = %v", err)
		}
		defer lock.Close()

		command := exec.Command(flockPath, "--exclusive", "--nonblock", lockPath, "true")
		command.Env = []string{"PATH=" + pathEnv, "HOME=" + tempDir}
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("flock unexpectedly acquired Go-held lock; output %q", output)
		}
	})
}

func waitForFlockHeld(t *testing.T, flockPath, lockPath, home, pathEnv string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.Command(flockPath, "--exclusive", "--nonblock", lockPath, "true")
		command.Env = []string{"PATH=" + pathEnv, "HOME=" + home}
		if err := command.Run(); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("flock subprocess did not acquire the lock before timeout")
}
