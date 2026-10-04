//go:build windows && (amd64 || arm64)

package main

import (
	"syscall"
	"unsafe"
)

func preparePlatform(c Config) {
	if c.HideConsole {
		kernel32.NewProc("FreeConsole").Call()
	}
}

func reportFatal(err error) {
	text, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString("Push-to-talk error")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
