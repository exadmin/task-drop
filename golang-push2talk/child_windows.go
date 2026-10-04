//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureChildProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
func stopChildProcess(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(p.Pid), "/T", "/F")
	configureChildProcess(command)
	if err := command.Run(); err != nil {
		return errors.Join(err, p.Kill())
	}
	return nil
}
