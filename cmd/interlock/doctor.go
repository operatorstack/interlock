package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
)

// doctorReport is the one-stop readiness picture: the binary + protocols, whether an
// update is available, which toolchains are present, whether a typed SDK is installed
// and in protocol-sync with this CLI, and whether the project's registry is
// configured. Rendered as text or, with --json, as a machine object.
type doctorReport struct {
	Version   string            `json:"version"`
	Protocols map[string]string `json:"protocols"`
	Update    string            `json:"update"` // "up-to-date" | "newer" | "unknown"
	Latest    string            `json:"latest,omitempty"`
	Toolchain map[string]bool   `json:"toolchain"`
	SDK       map[string]string `json:"sdk"`      // lang -> version | "not installed"
	Skew      []string          `json:"skew"`     // human-readable mismatch notes
	Registry  map[string]bool   `json:"registry"` // config artifact -> present
}

func cmdDoctor(args []string) error {
	jsonOut := false
	dir := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--dir":
			if i+1 >= len(args) {
				return fmt.Errorf("doctor: --dir wants a path")
			}
			dir = args[i+1]
			i++
		default:
			return fmt.Errorf("doctor: unexpected argument %q", args[i])
		}
	}

	rep := doctorReport{
		Version: releaseVersion(),
		Protocols: map[string]string{
			"policy": ir.Protocol, "effect": protocol.EffectRequestProtocol, "receipt": receipt.Schema,
		},
		Toolchain: map[string]bool{},
		SDK:       map[string]string{},
		Registry:  map[string]bool{},
	}

	// Update / connectivity.
	if latest, err := latestVersion(resolveGetHost("")); err != nil {
		rep.Update = "unknown"
	} else {
		rep.Latest = latest
		if upgradeAvailable(rep.Version, latest) {
			rep.Update = "newer"
		} else {
			rep.Update = "up-to-date"
		}
	}

	// Toolchains.
	for _, tool := range []string{"go", "node", "npm", "python", "python3", "uv", "pip", "pip3"} {
		_, err := exec.LookPath(tool)
		rep.Toolchain[tool] = err == nil
	}

	// Installed SDK versions + protocol skew (compatible == same release).
	compat := compatibleSDKVersion()
	if v := npmSDKVersion(dir); v != "" {
		rep.SDK["ts"] = v
		if compat != "" && v != compat {
			rep.Skew = append(rep.Skew, fmt.Sprintf("ts SDK %s != CLI %s — run: interlock install ts --upgrade", v, compat))
		}
	} else {
		rep.SDK["ts"] = "not installed"
	}
	if v := pySDKVersion(); v != "" {
		rep.SDK["python"] = v
		if compat != "" && v != compat {
			rep.Skew = append(rep.Skew, fmt.Sprintf("python SDK %s != CLI %s — run: interlock install python --upgrade", v, compat))
		}
	} else {
		rep.SDK["python"] = "not installed"
	}

	// Registry config present in this project.
	rep.Registry["npm"] = npmRegistryConfigured(dir)
	rep.Registry["pip"] = fileExists(filepath.Join(dir, ".interlock", "registry"))

	if jsonOut {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	printDoctorText(rep)
	return nil
}

func printDoctorText(rep doctorReport) {
	fmt.Printf("interlock doctor\n")
	fmt.Printf("  version         : %s\n", rep.Version)
	fmt.Printf("  policy protocol : %s\n", rep.Protocols["policy"])
	fmt.Printf("  effect protocol : %s\n", rep.Protocols["effect"])
	fmt.Printf("  receipt schema  : %s\n", rep.Protocols["receipt"])
	switch rep.Update {
	case "newer":
		fmt.Printf("  updates         : newer available %s -> %s (run: interlock upgrade)\n", rep.Version, rep.Latest)
	case "up-to-date":
		fmt.Printf("  updates         : up to date (latest %s)\n", rep.Latest)
	default:
		fmt.Printf("  updates         : could not check (offline?)\n")
	}
	fmt.Printf("  toolchains      : %s\n", toolchainSummary(rep.Toolchain))
	fmt.Printf("  ts SDK          : %s\n", rep.SDK["ts"])
	fmt.Printf("  python SDK      : %s\n", rep.SDK["python"])
	fmt.Printf("  registry config : npm=%v pip=%v\n", rep.Registry["npm"], rep.Registry["pip"])
	if len(rep.Skew) > 0 {
		for _, s := range rep.Skew {
			fmt.Printf("  SKEW            : %s\n", s)
		}
	}
	fmt.Printf("  note            : init --authoring json and test need no toolchain\n")
}

func toolchainSummary(tc map[string]bool) string {
	parts := []string{}
	for _, t := range []string{"go", "node", "npm", "python", "python3", "uv", "pip", "pip3"} {
		if tc[t] {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "(none found)"
	}
	return strings.Join(parts, ", ")
}

// npmSDKVersion reads the installed typed client's version from node_modules.
func npmSDKVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "node_modules", npmClientPkg, "package.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Version
}

// pySDKVersion reports the installed python client version via uv/pip show.
func pySDKVersion() string {
	for _, pm := range [][]string{{"uv", "pip", "show", pyClientPkg}, {"pip", "show", pyClientPkg}, {"pip3", "show", pyClientPkg}} {
		if _, err := exec.LookPath(pm[0]); err != nil {
			continue
		}
		out, err := exec.Command(pm[0], pm[1:]...).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Version:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
			}
		}
	}
	return ""
}

// npmRegistryConfigured reports whether the project .npmrc points the scope at us.
func npmRegistryConfigured(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ".npmrc"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), npmScope+":registry=")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
