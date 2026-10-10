//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// test build: OOZ env var points at a helper that inflates one Oodle block
func loadOodle(gameRoot string) (DecompressFunc, error) {
	tool := os.Getenv("OOZ")
	if tool == "" {
		return nil, fmt.Errorf("no Oodle decompressor (set OOZ)")
	}
	return func(src, dst []byte) error {
		cmd := exec.Command(tool, strconv.Itoa(len(dst)))
		cmd.Stdin = bytes.NewReader(src)
		out, err := cmd.Output()
		if err != nil || len(out) != len(dst) {
			return fmt.Errorf("ooz failed: %v (%d bytes)", err, len(out))
		}
		copy(dst, out)
		return nil
	}, nil
}

func processRunning(exe string) bool { return false }
func freeSpace(dir string) int64     { return 0 }
func hideWindow(cmd *exec.Cmd)       {}
