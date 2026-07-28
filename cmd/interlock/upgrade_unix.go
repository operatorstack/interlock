//go:build !windows

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func upgradeArchiveName(version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", binaryName, version, runtime.GOOS, runtime.GOARCH)
}

func extractUpgradeBinary(data []byte) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binaryName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

func applyUpgrade(bin []byte, version, host string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	dir := filepath.Dir(self)
	tmp := filepath.Join(dir, "."+binaryName+".upgrade")
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("upgrade: cannot write to %s (permission denied). Re-run with sudo, or reinstall: curl -fsSL https://%s/%s | sh", dir, resolveGetHost(host), binaryName)
		}
		return err
	}
	if err := os.Rename(tmp, self); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("upgrade: replace %s: %w", self, err)
	}
	fmt.Printf("upgraded to interlock %s at %s\n", version, self)
	return nil
}

func cmdApplyUpgrade([]string) error {
	return fmt.Errorf("internal Windows upgrade helper is unavailable")
}
