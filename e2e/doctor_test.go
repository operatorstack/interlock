package e2e

// control-law: shipped-surface-honors-the-core (doctor readiness)
//
// `interlock doctor` is the one-stop readiness picture: binary + protocols, update
// drift, toolchains, installed typed-SDK version + protocol skew, and registry
// config. These assert the machine (--json) surface + SDK detection hermetically.
// (Skew itself fires only on a released binary, where the CLI pins a concrete
// compatible version; the test binary is "dev" and pins nothing.)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourney_Doctor(t *testing.T) {
	t.Run("--json reports protocols, toolchains, sdk, registry", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr, code := run(t, "doctor", "--json", "--dir", dir)
		if code != 0 {
			t.Fatalf("doctor --json failed (%d): %s", code, stderr)
		}
		var rep struct {
			Version   string            `json:"version"`
			Protocols map[string]string `json:"protocols"`
			Toolchain map[string]bool   `json:"toolchain"`
			SDK       map[string]string `json:"sdk"`
			Registry  map[string]bool   `json:"registry"`
		}
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("doctor --json not valid JSON: %v\n%s", err, stdout)
		}
		if rep.Protocols["effect"] != "interlock.effect.v1" {
			t.Fatalf("missing effect protocol: %+v", rep.Protocols)
		}
		if rep.SDK["ts"] != "not installed" || rep.Registry["npm"] {
			t.Fatalf("empty project should report no SDK / no registry: %+v %+v", rep.SDK, rep.Registry)
		}
	})

	t.Run("detects an installed ts SDK version", func(t *testing.T) {
		dir := t.TempDir()
		pkgDir := filepath.Join(dir, "node_modules", "@operatorstack", "interlock")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@operatorstack/interlock","version":"9.9.9"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, _, code := run(t, "doctor", "--json", "--dir", dir)
		if code != 0 {
			t.Fatal("doctor failed")
		}
		var rep struct {
			SDK map[string]string `json:"sdk"`
		}
		json.Unmarshal([]byte(stdout), &rep)
		if rep.SDK["ts"] != "9.9.9" {
			t.Fatalf("expected ts SDK 9.9.9, got %q", rep.SDK["ts"])
		}
	})

	t.Run("reports registry config after install --configure-only", func(t *testing.T) {
		dir := t.TempDir()
		if _, stderr, code := run(t, "install", "ts", "--dir", dir, "--configure-only"); code != 0 {
			t.Fatalf("install ts failed: %s", stderr)
		}
		stdout, _, _ := run(t, "doctor", "--json", "--dir", dir)
		var rep struct {
			Registry map[string]bool `json:"registry"`
		}
		json.Unmarshal([]byte(stdout), &rep)
		if !rep.Registry["npm"] {
			t.Fatalf("registry.npm should be true after install: %s", stdout)
		}
	})

	t.Run("text output has the readiness lines", func(t *testing.T) {
		stdout, _, _ := run(t, "doctor", "--dir", t.TempDir())
		for _, want := range []string{"toolchains", "ts SDK", "python SDK", "registry config"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("doctor text missing %q:\n%s", want, stdout)
			}
		}
	})
}
