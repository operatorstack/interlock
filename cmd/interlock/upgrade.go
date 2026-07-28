package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// binaryName is the front-door package id and the on-disk executable name — the
// same "interlock" the shell installer uses.
const binaryName = "interlock"

// getBaseURL turns a host into a base URL. A bare host gets https:// (the normal
// case); a host that already carries a scheme is used verbatim, so tests can point
// the CLI at a local http server. Shared by upgrade and the doctor drift check.
func getBaseURL(host string) string {
	if strings.Contains(host, "://") {
		return strings.TrimRight(host, "/")
	}
	return "https://" + host
}

func httpGetBytes(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// latestVersion resolves the highest published version from the front door.
func latestVersion(host string) (string, error) {
	b, err := httpGetBytes(getBaseURL(host) + "/" + binaryName + "/latest")
	if err != nil {
		return "", err
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &payload); err != nil || payload.Version == "" {
		return "", fmt.Errorf("could not parse latest version")
	}
	return payload.Version, nil
}

// cmpSemver returns >0 if a>b, <0 if a<b, 0 if equal, over the leading x.y.z.
func cmpSemver(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// upgradeAvailable reports whether latest is newer than the running build. A "dev"
// (un-stamped) build always treats a real published version as newer.
func upgradeAvailable(current, latest string) bool {
	if current == "dev" || current == "" {
		return latest != ""
	}
	return cmpSemver(latest, current) > 0
}

func cmdUpgrade(args []string) error {
	host := ""
	checkOnly := false
	yes := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--host":
			if i+1 >= len(args) {
				return fmt.Errorf("upgrade: --host wants a hostname")
			}
			host = args[i+1]
			i += 2
		case "--check":
			checkOnly = true
			i++
		case "--yes", "-y":
			yes = true
			i++
		default:
			return fmt.Errorf("upgrade: unexpected argument %q", args[i])
		}
	}
	host = resolveGetHost(host)
	current := releaseVersion()

	latest, err := latestVersion(host)
	if err != nil {
		return fmt.Errorf("upgrade: resolve latest: %w", err)
	}
	if !upgradeAvailable(current, latest) {
		fmt.Printf("interlock %s is up to date (latest %s)\n", current, latest)
		return nil
	}
	if checkOnly {
		fmt.Printf("a newer interlock is available: %s -> %s (run: interlock upgrade)\n", current, latest)
		return nil
	}
	if !yes {
		fmt.Printf("upgrading interlock %s -> %s\n", current, latest)
	}
	return downloadAndReplace(host, latest)
}

// downloadAndReplace fetches the OS/arch archive + checksums for version, verifies
// the SHA-256 in-process, and atomically replaces the running executable.
func downloadAndReplace(host, version string) error {
	archive := upgradeArchiveName(version)
	base := fmt.Sprintf("%s/%s/dl/%s", getBaseURL(host), binaryName, version)

	archiveBytes, err := httpGetBytes(base + "/" + archive)
	if err != nil {
		return fmt.Errorf("upgrade: download %s: %w", archive, err)
	}
	checksums, err := httpGetBytes(base + "/checksums.txt")
	if err != nil {
		return fmt.Errorf("upgrade: download checksums: %w", err)
	}
	want := checksumFor(string(checksums), archive)
	if want == "" {
		return fmt.Errorf("upgrade: no checksum listed for %s", archive)
	}
	sum := sha256.Sum256(archiveBytes)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("upgrade: checksum mismatch for %s (want %s, got %s) — refusing", archive, want, got)
	}

	bin, err := extractUpgradeBinary(archiveBytes)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	return applyUpgrade(bin, version, host)
}

// checksumFor returns the hex digest listed for name in a "sha  name" manifest.
func checksumFor(manifest, name string) string {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}
