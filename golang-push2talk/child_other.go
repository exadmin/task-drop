//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func configureChildProcess(*exec.Cmd) {}
func stopChildProcess(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	return p.Kill()
}
