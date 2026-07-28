package e2e

// control-law: shipped-surface-honors-the-core (upgrade)
//
// `interlock upgrade` self-updates the binary from the front door: resolve /latest,
// download the OS/arch archive + checksums, verify SHA-256, atomically replace the
// running executable. Hermetic — a local stub server stands in for the front door
// (via --host http://…), a throwaway copy of the binary is the upgrade target so the
// shared test binary is never clobbered, and the "fake" payload is asserted in place.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubFrontDoor serves /interlock/latest and /interlock/dl/<version>/{archive,checksums.txt}
// for the given version, with a tar.gz whose "interlock" entry contains payload.
func stubFrontDoor(t *testing.T, version string, payload []byte) *httptest.Server {
	return stubFrontDoorWithChecksum(t, version, payload, true)
}

func stubFrontDoorWithChecksum(t *testing.T, version string, payload []byte, valid bool) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	archive := fmt.Sprintf("interlock_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		archive = fmt.Sprintf("interlock_%s_windows_%s.zip", version, runtime.GOARCH)
		zw := zip.NewWriter(&buf)
		entry, err := zw.Create("interlock.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "interlock", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	tgz := buf.Bytes()
	sum := sha256.Sum256(tgz)
	if !valid {
		sum[0] ^= 0xff
	}
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archive)

	mux := http.NewServeMux()
	mux.HandleFunc("/interlock/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":%q}`, version)
	})
	mux.HandleFunc("/interlock/dl/"+version+"/"+archive, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(tgz)
	})
	mux.HandleFunc("/interlock/dl/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestJourney_Upgrade(t *testing.T) {
	// The test binary is built without ldflags, so its version is "dev" — always
	// upgrade-eligible against any published version.
	srv := stubFrontDoor(t, "9.9.9", []byte("FAKE-UPGRADED-INTERLOCK\n"))

	t.Run("--check reports a newer version", func(t *testing.T) {
		stdout, stderr, code := run(t, "upgrade", "--check", "--host", srv.URL)
		if code != 0 {
			t.Fatalf("upgrade --check failed (%d): %s", code, stderr)
		}
		if !strings.Contains(stdout, "9.9.9") || !strings.Contains(stdout, "newer") {
			t.Fatalf("--check did not report the newer version: %s", stdout)
		}
	})

	t.Run("real upgrade replaces the binary atomically", func(t *testing.T) {
		// Copy the test binary so os.Executable() points at a throwaway target.
		dir := t.TempDir()
		target := filepath.Join(dir, "interlock")
		if runtime.GOOS == "windows" {
			target += ".exe"
		}
		src, err := os.ReadFile(interlockBin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, src, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(target, "upgrade", "--host", srv.URL, "--yes")
		result := filepath.Join(dir, "upgrade-result")
		cmd.Env = append(os.Environ(), "INTERLOCK_UPGRADE_RESULT="+result)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("upgrade failed: %v\n%s", err, out)
		}
		if runtime.GOOS == "windows" {
			deadline := time.Now().Add(10 * time.Second)
			for {
				status, readErr := os.ReadFile(result)
				if readErr == nil {
					if string(status) != "ok\n" {
						t.Fatalf("upgrade helper failed: %s", status)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("timed out waiting for upgrade helper: %v", readErr)
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "FAKE-UPGRADED-INTERLOCK\n" {
			t.Fatalf("binary not replaced with the fetched payload; got %q", string(got))
		}
	})

	t.Run("offline resolve fails closed", func(t *testing.T) {
		// A host with no server → resolve error, non-zero exit, no partial write.
		_, _, code := run(t, "upgrade", "--check", "--host", "http://127.0.0.1:1")
		if code == 0 {
			t.Fatal("upgrade against an unreachable host should fail")
		}
	})

	t.Run("checksum mismatch fails before replacing the binary", func(t *testing.T) {
		bad := stubFrontDoorWithChecksum(t, "9.9.10", []byte("UNTRUSTED\n"), false)
		dir := t.TempDir()
		target := filepath.Join(dir, "interlock")
		if runtime.GOOS == "windows" {
			target += ".exe"
		}
		original, err := os.ReadFile(interlockBin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, original, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(target, "upgrade", "--host", bad.URL, "--yes")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "checksum mismatch") {
			t.Fatalf("want checksum failure, got err=%v output=%s", err, out)
		}
		after, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, original) {
			t.Fatal("checksum failure modified the installed binary")
		}
	})
}
