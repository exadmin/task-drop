//go:build windows && (amd64 || arm64)

package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const trayMessage = 0x8001
const trayOpenDirectory = 1
const trayExit = 2

var shell32 = syscall.NewLazyDLL("shell32.dll")
var shellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")

type notifyIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                guid
	BalloonIcon         uintptr
}

type trayIcon struct {
	data           notifyIconData
	restartMessage uint32
	added          bool
}

func copyUTF16(dst []uint16, text string) {
	clear(dst)
	encoded := utf16.Encode([]rune(text))
	if len(encoded) >= len(dst) {
		encoded = encoded[:len(dst)-1]
		if len(encoded) > 0 && encoded[len(encoded)-1] >= 0xd800 && encoded[len(encoded)-1] <= 0xdbff {
			encoded = encoded[:len(encoded)-1]
		}
	}
	copy(dst, encoded)
}

func createMicrophoneIcon() (uintptr, error) {
	// Monochrome AND/XOR masks draw a microphone against a transparent background.
	and := [128]byte{}
	xor := [128]byte{}
	for i := range and {
		and[i] = 0xff
	}
	pixel := func(x, y int) {
		offset := (31-y)*4 + x/8
		bit := byte(0x80 >> (x % 8))
		and[offset] &^= bit
		xor[offset] |= bit
	}
	for y := 4; y <= 19; y++ {
		for x := 12; x <= 19; x++ {
			if (y > 5 && y < 18) || (x > 13 && x < 18) {
				pixel(x, y)
			}
		}
	}
	for y := 13; y <= 21; y++ {
		pixel(8, y)
		pixel(9, y)
		pixel(22, y)
		pixel(23, y)
	}
	for x := 10; x <= 21; x++ {
		pixel(x, 22)
		pixel(x, 23)
	}
	for y := 24; y <= 27; y++ {
		pixel(15, y)
		pixel(16, y)
	}
	for x := 10; x <= 21; x++ {
		pixel(x, 28)
		pixel(x, 29)
	}
	icon, _, err := user32.NewProc("CreateIcon").Call(0, 32, 32, 1, 1, uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&xor[0])))
	if icon == 0 {
		return 0, fmt.Errorf("create tray icon: %w", err)
	}
	return icon, nil
}

func newTray(window uintptr, hotkey string) (*trayIcon, error) {
	icon, err := createTrayImageIcon()
	if err != nil {
		return nil, err
	}
	name, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	restart, _, err := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(name)))
	if restart == 0 {
		user32.NewProc("DestroyIcon").Call(icon)
		return nil, fmt.Errorf("register tray recovery message: %w", err)
	}
	t := &trayIcon{data: notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1, Flags: 7, Callback: trayMessage, Icon: icon}, restartMessage: uint32(restart)}
	copyUTF16(t.data.Tip[:], "Push-to-talk | Hold "+hotkey)
	if err = t.add(); err != nil {
		t.close()
		return nil, err
	}
	return t, nil
}

func (t *trayIcon) add() error {
	ok, _, err := shellNotifyIcon.Call(0, uintptr(unsafe.Pointer(&t.data)))
	t.added = ok != 0
	if !t.added {
		return fmt.Errorf("add System Tray icon: %w", err)
	}
	version := t.data
	version.Version = 4
	// Version 4 delivers keyboard selection and context-menu notifications.
	ok, _, err = shellNotifyIcon.Call(4, uintptr(unsafe.Pointer(&version)))
	if ok == 0 {
		shellNotifyIcon.Call(2, uintptr(unsafe.Pointer(&t.data)))
		t.added = false
		return fmt.Errorf("configure System Tray icon: %w", err)
	}
	return nil
}

func (t *trayIcon) close() {
	if t.added {
		shellNotifyIcon.Call(2, uintptr(unsafe.Pointer(&t.data)))
		t.added = false
	}
	if t.data.Icon != 0 {
		user32.NewProc("DestroyIcon").Call(t.data.Icon)
		t.data.Icon = 0
	}
}

func (t *trayIcon) notifyError(err error) {
	if t == nil || !t.added {
		return
	}
	data := t.data
	data.Flags = 0x10
	data.InfoFlags = 3
	copyUTF16(data.InfoTitle[:], "Push-to-talk error")
	copyUTF16(data.Info[:], err.Error())
	shellNotifyIcon.Call(1, uintptr(unsafe.Pointer(&data)))
}

func (a *application) trayMenu() {
	menu, _, err := user32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		log.Printf("Create tray menu failed: %v", err)
		return
	}
	defer user32.NewProc("DestroyMenu").Call(menu)
	appendMenu := user32.NewProc("AppendMenuW")
	for _, item := range []struct {
		id   uintptr
		text string
	}{{trayOpenDirectory, "Open recordings folder"}, {trayExit, "Exit"}} {
		text, _ := syscall.UTF16PtrFromString(item.text)
		appendMenu.Call(menu, 0, item.id, uintptr(unsafe.Pointer(text)))
	}
	var cursor point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	// Activation here follows an explicit click on the tray icon, not recording.
	user32.NewProc("SetForegroundWindow").Call(a.window)
	command, _, _ := user32.NewProc("TrackPopupMenu").Call(menu, 0x182, uintptr(cursor.X), uintptr(cursor.Y), 0, a.window, 0)
	postMessage.Call(a.window, 0, 0, 0)
	shellNotifyIcon.Call(3, uintptr(unsafe.Pointer(&a.tray.data)))
	a.handleTrayCommand(command)
}

func (a *application) handleTrayCommand(command uintptr) {
	switch command {
	case trayOpenDirectory:
		if err := os.MkdirAll(a.config.OutputDirectory, 0755); err != nil {
			log.Print(err)
			a.tray.notifyError(err)
			return
		}
		verb, _ := syscall.UTF16PtrFromString("open")
		path, _ := syscall.UTF16PtrFromString(a.config.OutputDirectory)
		result, _, _ := shell32.NewProc("ShellExecuteW").Call(a.window, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(path)), 0, 0, 1)
		if result <= 32 {
			err := fmt.Errorf("open recordings folder: Windows shell error %d", result)
			log.Print(err)
			a.tray.notifyError(err)
		}
	case trayExit:
		postMessage.Call(a.window, 0x10, 0, 0)
	}
}
