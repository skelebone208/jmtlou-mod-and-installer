//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	pGetDiskFreeEx   = kernel32.NewProc("GetDiskFreeSpaceExW")
	oodleProcs       = map[string]*syscall.Proc{}
)

func loadOodle(gameRoot string) (DecompressFunc, error) {
	matches, _ := filepath.Glob(filepath.Join(gameRoot, "oo2core_*_win64.dll"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("oo2core_9_win64.dll wasn't found in the game folder - is the game fully installed?")
	}
	path := matches[len(matches)-1]
	proc := oodleProcs[path]
	if proc == nil {
		dll, err := syscall.LoadDLL(path)
		if err != nil {
			return nil, fmt.Errorf("couldn't load %s: %v", filepath.Base(path), err)
		}
		proc, err = dll.FindProc("OodleLZ_Decompress")
		if err != nil {
			return nil, err
		}
		oodleProcs[path] = proc
	}
	return func(src, dst []byte) error {
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)),
			uintptr(unsafe.Pointer(&dst[0])), uintptr(len(dst)),
			1, 0, 0, 0, 0, 0, 0, 0, 0, 3)
		if int64(r) != int64(len(dst)) {
			return fmt.Errorf("Oodle returned %d, expected %d", int64(r), len(dst))
		}
		return nil
	}, nil
}

func processRunning(exe string) bool {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if strings.EqualFold(syscall.UTF16ToString(e.ExeFile[:]), exe) {
			return true
		}
	}
	return false
}

func freeSpace(dir string) int64 {
	var free uint64
	p, _ := syscall.UTF16PtrFromString(dir)
	r, _, _ := pGetDiskFreeEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), 0, 0)
	if r == 0 {
		return 0
	}
	return int64(free)
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
