package e2e

// control-law: shipped-surface-honors-the-core (install)
//
// `interlock install <lang>` configures the consumer's package manager to fetch the
// typed client from the project's front-door registry — no hand-edited .npmrc /
// --index-url, no GCP credentials. --configure-only writes the registry config
// without invoking a toolchain, so this journey is hermetic (no npm/pip/network):
// it asserts the exact config the command writes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourney_Install(t *testing.T) {
	host := "get.operatorstack.systems"

	t.Run("typescript writes a scoped npm registry", func(t *testing.T) {
		dir := t.TempDir()
		_, stderr, code := run(t, "install", "ts", "--dir", dir, "--configure-only", "--host", host)
		if code != 0 {
			t.Fatalf("install ts failed (%d): %s", code, stderr)
		}
		b, err := os.ReadFile(filepath.Join(dir, ".npmrc"))
		if err != nil {
			t.Fatalf("read .npmrc: %v", err)
		}
		want := "@operatorstack:registry=https://" + host + "/npm/"
		if !strings.Contains(string(b), want) {
			t.Fatalf(".npmrc missing scoped registry\nwant: %s\ngot:\n%s", want, b)
		}
		if strings.Contains(string(b), "pkg.dev") {
			t.Fatalf(".npmrc leaks the private AR host: %s", b)
		}
	})

	t.Run("re-run is idempotent (one registry line)", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "install", "ts", "--dir", dir, "--configure-only", "--host", host)
		run(t, "install", "ts", "--dir", dir, "--configure-only", "--host", host)
		b, _ := os.ReadFile(filepath.Join(dir, ".npmrc"))
		if n := strings.Count(string(b), "@operatorstack:registry="); n != 1 {
			t.Fatalf("expected exactly one registry line, got %d:\n%s", n, b)
		}
	})

	t.Run("python writes a pip index pointing at the front door", func(t *testing.T) {
		dir := t.TempDir()
		_, stderr, code := run(t, "install", "python", "--dir", dir, "--configure-only", "--host", host)
		if code != 0 {
			t.Fatalf("install python failed (%d): %s", code, stderr)
		}
		b, err := os.ReadFile(filepath.Join(dir, ".interlock", "registry"))
		if err != nil {
			t.Fatalf("read .interlock/registry: %v", err)
		}
		if !strings.Contains(string(b), "PIP_INDEX_URL=https://"+host+"/pip/simple/") {
			t.Fatalf("registry file missing pip index:\n%s", b)
		}
	})

	t.Run("no language, non-interactive, fails closed", func(t *testing.T) {
		_, _, code := run(t, "install")
		if code == 0 {
			t.Fatal("install with no language and no stdin should fail closed")
		}
	})

	t.Run("unknown language fails closed", func(t *testing.T) {
		_, _, code := run(t, "install", "rust", "--configure-only")
		if code == 0 {
			t.Fatal("unknown language should fail closed")
		}
	})
}

func TestJourney_InstallLifecycle(t *testing.T) {
	t.Run("--revert removes the npm registry config", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "install", "ts", "--dir", dir, "--configure-only")
		if _, err := os.Stat(filepath.Join(dir, ".npmrc")); err != nil {
			t.Fatalf(".npmrc not written: %v", err)
		}
		if _, stderr, code := run(t, "install", "ts", "--dir", dir, "--revert"); code != 0 {
			t.Fatalf("revert failed: %s", stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, ".npmrc")); !os.IsNotExist(err) {
			t.Fatalf(".npmrc should be gone after revert")
		}
	})

	t.Run("--revert removes the python registry config", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "install", "python", "--dir", dir, "--configure-only")
		run(t, "install", "python", "--dir", dir, "--revert")
		if _, err := os.Stat(filepath.Join(dir, ".interlock", "registry")); !os.IsNotExist(err) {
			t.Fatalf(".interlock/registry should be gone after revert")
		}
	})

	t.Run("--example writes a starter, refuses overwrite without --force", func(t *testing.T) {
		dir := t.TempDir()
		if _, stderr, code := run(t, "install", "ts", "--dir", dir, "--example"); code != 0 {
			t.Fatalf("example failed: %s", stderr)
		}
		b, err := os.ReadFile(filepath.Join(dir, "interlock-example.ts"))
		if err != nil || !strings.Contains(string(b), "interlock.effect.v1") {
			t.Fatalf("example not written correctly: %v", err)
		}
		if _, _, code := run(t, "install", "ts", "--dir", dir, "--example"); code == 0 {
			t.Fatal("second --example without --force should fail")
		}
		if _, _, code := run(t, "install", "ts", "--dir", dir, "--example", "--force"); code != 0 {
			t.Fatal("--example --force should overwrite")
		}
	})
}
