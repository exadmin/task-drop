//go:build windows && (amd64 || arm64)

package main

import (
	"fmt"
	"math"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestAudioLevels(t *testing.T) {
	pcm := []byte{1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 16, 0, 0, 0}
	if level := audioLevel([]byte{0, 0x40, 0, 0x80}, pcm); level != 1 {
		t.Fatalf("PCM peak: %f", level)
	}
	pcm[0] = 3
	pcm[14] = 32
	pcm[12] = 4
	if level := audioLevel([]byte{0, 0, 0, 0x3f}, pcm); math.Abs(level-0.5) > 0.001 {
		t.Fatalf("float peak: %f", level)
	}
}

func TestNotificationTransitions(t *testing.T) {
	now := time.Now()
	a := application{serviceLoading: true}
	if got := a.notificationText(now); got != "Приложение загружается…" {
		t.Fatal(got)
	}
	a.serviceLoading = false
	a.readyUntil = now.Add(3 * time.Second)
	if got := a.notificationText(now); got != "Приложение готово" {
		t.Fatal(got)
	}
	if got := a.notificationText(now.Add(3 * time.Second)); got != "" {
		t.Fatalf("ready notification did not expire: %q", got)
	}
	a.displayText = "Previous result"
	a.displayQueue = []string{"Previous queued result"}
	a.displayUntil = now.Add(5 * time.Second)
	a.clearDisplay()
	a.transcriptionBusy = true
	if got := a.notificationText(now); got != "Идёт транскрибация…" {
		t.Fatalf("previous result survived a new recording: %q", got)
	}
	if len(a.displayQueue) != 0 || !a.displayUntil.IsZero() {
		t.Fatal("previous display state was retained")
	}
	a.transcriptionBusy = false
	a.displayText = "New result"
	if got := a.notificationText(now); got != "New result" {
		t.Fatal(got)
	}
}

// This opt-in test opens the overlay without activating the microphone.
func TestWindowsOverlayLifecycle(t *testing.T) {
	if os.Getenv("P2T_UI_TEST") != "1" {
		t.Skip("set P2T_UI_TEST=1 to test the native window")
	}
	c, err := loadConfig("properties.json.example")
	if err != nil {
		t.Fatal(err)
	}
	c.HotKey = "Ctrl+Alt+Shift+9"
	c.Transcription.DisplaySeconds = 5
	c.Transcription.Enabled = false
	checked := make(chan error, 1)
	go func() {
		find := user32.NewProc("FindWindowW")
		name, _ := syscall.UTF16PtrFromString(fmt.Sprintf("GoPushToTalkOverlay-%d", os.Getpid()))
		deadline := time.Now().Add(5 * time.Second)
		var window uintptr
		for time.Now().Before(deadline) {
			window, _, _ = find.Call(uintptr(unsafe.Pointer(name)), 0)
			if window != 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if window == 0 {
			checked <- fmt.Errorf("overlay was not created")
			return
		}
		// Wait until registration and the message loop are ready.
		time.Sleep(100 * time.Millisecond)
		before, _, _ := getForegroundWindow.Call()
		showWindow.Call(window, 4)
		after, _, _ := getForegroundWindow.Call()
		style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(window, ^uintptr(19))
		var checkErr error
		var alpha byte
		var flags uint32
		ok, _, _ := user32.NewProc("GetLayeredWindowAttributes").Call(window, 0, uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags)))
		if ok == 0 || int(alpha) != c.Overlay.Opacity || flags&2 == 0 {
			checkErr = fmt.Errorf("overlay opacity was not applied: %d", alpha)
		}
		if before != after {
			checkErr = fmt.Errorf("overlay changed the foreground window")
		}
		if style&0x08000008 != 0x08000008 {
			checkErr = fmt.Errorf("overlay lacks no-activate or topmost styles")
		}
		identifier := struct {
			Size   uint32
			Window uintptr
			ID     uint32
			GUID   guid
		}{Window: window, ID: 1}
		identifier.Size = uint32(unsafe.Sizeof(identifier))
		var iconRect rect
		hr, _, _ := shell32.NewProc("Shell_NotifyIconGetRect").Call(uintptr(unsafe.Pointer(&identifier)), uintptr(unsafe.Pointer(&iconRect)))
		if int32(hr) < 0 {
			checkErr = fmt.Errorf("tray icon is not registered: 0x%08X", uint32(hr))
		}
		// Simulate Explorer losing this icon without restarting the user's shell.
		data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1}
		shellNotifyIcon.Call(2, uintptr(unsafe.Pointer(&data)))
		restartName, _ := syscall.UTF16PtrFromString("TaskbarCreated")
		restart, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(restartName)))
		postMessage.Call(window, restart, 0, 0)
		deadline = time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			hr, _, _ = shell32.NewProc("Shell_NotifyIconGetRect").Call(uintptr(unsafe.Pointer(&identifier)), uintptr(unsafe.Pointer(&iconRect)))
			if int32(hr) >= 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if int32(hr) < 0 {
			checkErr = fmt.Errorf("tray icon was not restored")
		}
		showWindow.Call(window, 0)
		before, _, _ = getForegroundWindow.Call()
		currentApp.transcribed <- transcriptionResult{text: "Проверка результата транскрибации. Текст должен отображаться пять секунд без перехвата фокуса."}
		visible := user32.NewProc("IsWindowVisible")
		deadline = time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			shown, _, _ := visible.Call(window)
			if shown != 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		shown, _, _ := visible.Call(window)
		if shown == 0 {
			checkErr = fmt.Errorf("transcript overlay was not shown")
		}
		after, _, _ = getForegroundWindow.Call()
		if before != after {
			checkErr = fmt.Errorf("transcript overlay changed focus")
		}
		time.Sleep(4 * time.Second)
		shown, _, _ = visible.Call(window)
		if shown == 0 {
			checkErr = fmt.Errorf("transcript overlay disappeared before five seconds")
		}
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			shown, _, _ = visible.Call(window)
			if shown == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if shown != 0 {
			checkErr = fmt.Errorf("transcript overlay did not expire")
		}
		postMessage.Call(window, 0x111, trayExit, 0)
		checked <- checkErr
	}()
	if err = run(c); err != nil {
		t.Fatal(err)
	}
	if err = <-checked; err != nil {
		t.Fatal(err)
	}
}

func TestTrayNativeLayout(t *testing.T) {
	if size := unsafe.Sizeof(notifyIconData{}); size != 976 {
		t.Fatalf("NOTIFYICONDATAW size: %d", size)
	}
	var dst [4]uint16
	copyUTF16(dst[:], "ab😀")
	if dst != [4]uint16{'a', 'b', 0, 0} {
		t.Fatalf("UTF-16 truncation split a surrogate pair: %v", dst)
	}
}
