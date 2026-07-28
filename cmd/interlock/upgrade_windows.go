//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess      = kernel32.NewProc("OpenProcess")
	procWaitForSingleObj = kernel32.NewProc("WaitForSingleObject")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
	procReplaceFile      = kernel32.NewProc("ReplaceFileW")
	procMoveFileEx       = kernel32.NewProc("MoveFileExW")
)

const (
	synchronize              = 0x00100000
	waitObject0              = 0
	replaceWriteThrough      = 0x00000001
	moveFileDelayUntilReboot = 0x00000004
)

func upgradeArchiveName(version string) string {
	return fmt.Sprintf("%s_%s_windows_%s.zip", binaryName, version, runtime.GOARCH)
}

func extractUpgradeBinary(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	for _, file := range zr.File {
		if filepath.Base(file.Name) != binaryName+".exe" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName+".exe")
}

func applyUpgrade(bin []byte, version, _ string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	dir := filepath.Dir(self)
	staged := filepath.Join(dir, "."+binaryName+".upgrade.exe")
	backup := filepath.Join(dir, "."+binaryName+".backup.exe")
	_ = os.Remove(backup)
	if err := os.WriteFile(staged, bin, 0o755); err != nil {
		return fmt.Errorf("upgrade: stage replacement: %w", err)
	}
	helper, err := copyUpgradeHelper(self)
	if err != nil {
		os.Remove(staged)
		return err
	}
	result := os.Getenv("INTERLOCK_UPGRADE_RESULT")
	cmd := exec.Command(helper, "__apply-upgrade",
		strconv.Itoa(os.Getpid()), staged, self, backup, result, helper)
	if err := cmd.Start(); err != nil {
		os.Remove(staged)
		os.Remove(helper)
		return fmt.Errorf("upgrade: start replacement helper: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("upgrade: detach replacement helper: %w", err)
	}
	fmt.Printf("interlock %s upgrade safely scheduled for %s\n", version, self)
	return nil
}

func copyUpgradeHelper(self string) (string, error) {
	src, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.CreateTemp("", "interlock-upgrade-helper-*.exe")
	if err != nil {
		return "", err
	}
	name := dst.Name()
	if _, err = io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(name)
		return "", err
	}
	if err = dst.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func cmdApplyUpgrade(args []string) error {
	if len(args) != 6 {
		return fmt.Errorf("invalid internal upgrade request")
	}
	parentPID, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	staged, target, backup, result, helper := args[1], args[2], args[3], args[4], args[5]
	err = waitForProcess(parentPID, 30*time.Second)
	if err == nil {
		err = replaceFile(target, staged, backup)
	}
	if err == nil {
		os.Remove(backup)
	}
	writeUpgradeResult(result, err)
	os.Remove(staged)
	scheduleDelete(helper)
	return err
}

func waitForProcess(pid int, timeout time.Duration) error {
	handle, _, callErr := procOpenProcess.Call(synchronize, 0, uintptr(uint32(pid)))
	if handle == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.Errno(87) {
			return nil // the parent exited before the helper opened its handle
		}
		return fmt.Errorf("open parent process: %w", callErr)
	}
	defer procCloseHandle.Call(handle)
	status, _, callErr := procWaitForSingleObj.Call(handle, uintptr(timeout/time.Millisecond))
	if status != waitObject0 {
		return fmt.Errorf("wait for parent process: status %d: %w", status, callErr)
	}
	return nil
}

func replaceFile(target, staged, backup string) error {
	targetp, _ := syscall.UTF16PtrFromString(target)
	stagedp, _ := syscall.UTF16PtrFromString(staged)
	backupp, _ := syscall.UTF16PtrFromString(backup)
	ok, _, callErr := procReplaceFile.Call(
		uintptr(unsafe.Pointer(targetp)),
		uintptr(unsafe.Pointer(stagedp)),
		uintptr(unsafe.Pointer(backupp)),
		replaceWriteThrough, 0, 0,
	)
	if ok == 0 {
		return fmt.Errorf("atomic replace: %w", callErr)
	}
	return nil
}

func writeUpgradeResult(path string, err error) {
	if path == "" {
		return
	}
	value := "ok\n"
	if err != nil {
		value = "error: " + err.Error() + "\n"
	}
	_ = os.WriteFile(path, []byte(value), 0o600)
}

func scheduleDelete(path string) {
	pathp, _ := syscall.UTF16PtrFromString(path)
	procMoveFileEx.Call(uintptr(unsafe.Pointer(pathp)), 0, moveFileDelayUntilReboot)
}
