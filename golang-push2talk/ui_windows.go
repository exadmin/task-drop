//go:build windows && (amd64 || arm64)

package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var gdi32 = syscall.NewLazyDLL("gdi32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var registerClass = user32.NewProc("RegisterClassExW")
var createWindow = user32.NewProc("CreateWindowExW")
var defWindowProc = user32.NewProc("DefWindowProcW")
var destroyWindow = user32.NewProc("DestroyWindow")
var getMessage = user32.NewProc("GetMessageW")
var translateMessage = user32.NewProc("TranslateMessage")
var dispatchMessage = user32.NewProc("DispatchMessageW")
var postMessage = user32.NewProc("PostMessageW")
var postQuit = user32.NewProc("PostQuitMessage")
var setTimer = user32.NewProc("SetTimer")
var killTimer = user32.NewProc("KillTimer")
var registerHotKey = user32.NewProc("RegisterHotKey")
var unregisterHotKey = user32.NewProc("UnregisterHotKey")
var getKeyState = user32.NewProc("GetAsyncKeyState")
var showWindow = user32.NewProc("ShowWindow")
var setWindowPos = user32.NewProc("SetWindowPos")
var invalidateRect = user32.NewProc("InvalidateRect")
var getClientRect = user32.NewProc("GetClientRect")
var beginPaint = user32.NewProc("BeginPaint")
var endPaint = user32.NewProc("EndPaint")
var fillRect = user32.NewProc("FillRect")
var drawText = user32.NewProc("DrawTextW")
var getForegroundWindow = user32.NewProc("GetForegroundWindow")
var monitorFromWindow = user32.NewProc("MonitorFromWindow")
var getMonitorInfo = user32.NewProc("GetMonitorInfoW")
var setLayered = user32.NewProc("SetLayeredWindowAttributes")
var getDPI = user32.NewProc("GetDpiForWindow")
var createBrush = gdi32.NewProc("CreateSolidBrush")
var deleteObject = gdi32.NewProc("DeleteObject")
var setTextColor = gdi32.NewProc("SetTextColor")
var setBkMode = gdi32.NewProc("SetBkMode")
var selectObject = gdi32.NewProc("SelectObject")
var getStockObject = gdi32.NewProc("GetStockObject")

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                                                        uint32
	Procedure                                                          uintptr
	ClassExtra, WindowExtra                                            int32
	Instance, Icon, Cursor, Background, MenuName, ClassName, SmallIcon uintptr
}
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Paint              rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}
type recordingResult struct {
	path string
	err  error
}
type application struct {
	config               Config
	hotkey               HotKey
	window               uintptr
	cancel               context.CancelFunc
	result               chan recordingResult
	level                atomic.Uint64
	recording            atomic.Bool
	closing              bool
	started              time.Time
	lastPaint            time.Time
	tray                 *trayIcon
	lastTrayRetry        time.Time
	transcriptionContext context.Context
	stopTranscription    context.CancelFunc
	workers              sync.WaitGroup
	transcribed          chan transcriptionResult
	transcriptionQueue   []string
	transcriptionBusy    bool
	displayQueue         []string
	displayText          string
	displayUntil         time.Time
	visibleText          string
	serviceLoading       bool
	readyUntil           time.Time
	python               *pythonService
	serviceStatus        chan serviceEvent
}

var currentApp *application

func keyDown(key uint32) bool {
	value, _, _ := getKeyState.Call(uintptr(key))
	return value&0x8000 != 0
}
func (a *application) held() bool {
	if !keyDown(a.hotkey.Key) {
		return false
	}
	for _, m := range []struct{ flag, key uint32 }{{2, 0x11}, {1, 0x12}, {4, 0x10}} {
		if a.hotkey.Modifiers&m.flag != 0 && !keyDown(m.key) {
			return false
		}
	}
	return a.hotkey.Modifiers&8 == 0 || keyDown(0x5b) || keyDown(0x5c)
}

func (a *application) position() {
	foreground, _, _ := getForegroundWindow.Call()
	monitor, _, _ := monitorFromWindow.Call(foreground, 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return
	}
	dpi, _, _ := getDPI.Call(a.window)
	if dpi == 0 {
		dpi = 96
	}
	scale := func(v int) int32 { return int32(v) * int32(dpi) / 96 }
	width, height := scale(a.config.Overlay.Width), scale(a.config.Overlay.Height)
	if a.visibleText != "" && a.cancel == nil {
		width = min(scale(a.config.Transcription.Width), info.Work.Right-info.Work.Left-24)
		dc, _, _ := user32.NewProc("GetDC").Call(a.window)
		font, _, _ := getStockObject.Call(17)
		old, _, _ := selectObject.Call(dc, font)
		text, _ := syscall.UTF16PtrFromString(a.visibleText)
		box := rect{0, 0, width - 24, 0}
		drawText.Call(dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&box)), 0xc10)
		selectObject.Call(dc, old)
		user32.NewProc("ReleaseDC").Call(a.window, dc)
		height = min(max(scale(80), box.Bottom+24), scale(a.config.Transcription.MaxHeight))
		height = min(height, info.Work.Bottom-info.Work.Top-24)
	}
	x := info.Work.Left + (info.Work.Right-info.Work.Left-width)/2
	y := info.Work.Bottom - height - scale(a.config.Overlay.BottomMargin)
	setWindowPos.Call(a.window, ^uintptr(0), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x10)
}

func (a *application) start() {
	if a.cancel != nil || a.closing || !a.held() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.recording.Store(false)
	a.level.Store(0)
	a.started = time.Now()
	a.clearDisplay()
	a.showOverlay("")
	go func() {
		path, err := record(ctx, a.config, &a.level, func() { a.recording.Store(true) })
		a.recording.Store(false)
		a.result <- recordingResult{path, err}
	}()
}

func (a *application) tick() {
	select {
	case event := <-a.serviceStatus:
		if !a.closing {
			a.serviceLoading = event.loading
			if event.err != nil {
				log.Printf("Python service startup failed: %v", event.err)
				a.tray.notifyError(event.err)
			} else if !event.loading {
				a.readyUntil = time.Now().Add(3 * time.Second)
				log.Print("Python service ready")
			}
		}
	default:
	}
	if a.tray != nil && !a.tray.added && !a.closing && time.Since(a.lastTrayRetry) >= 5*time.Second {
		a.lastTrayRetry = time.Now()
		if err := a.tray.add(); err != nil {
			log.Print(err)
		}
	}
	if a.cancel != nil && !a.held() {
		a.cancel()
		showWindow.Call(a.window, 0)
	}
	select {
	case result := <-a.result:
		a.cancel()
		a.cancel = nil
		showWindow.Call(a.window, 0)
		if result.err != nil {
			log.Printf("Recording failed: %v", result.err)
			a.tray.notifyError(result.err)
		} else if result.path != "" {
			log.Printf("Saved %s", result.path)
			if a.config.Transcription.Enabled && !a.closing {
				a.transcriptionQueue = append(a.transcriptionQueue, result.path)
			}
		}
		if a.closing {
			destroyWindow.Call(a.window)
		}
	default:
	}
	select {
	case result := <-a.transcribed:
		a.transcriptionBusy = false
		if !a.closing {
			if result.err != nil {
				log.Printf("Transcription failed for %s: %v", result.path, result.err)
				a.tray.notifyError(result.err)
			} else {
				text := result.text
				if text == "" {
					text = "No speech recognized."
				}
				a.displayQueue = append(a.displayQueue, text)
				log.Printf("Transcription completed for %s", result.path)
			}
		}
	default:
	}
	a.nextTranscription()
	a.updateDisplay(time.Now())
	if a.cancel != nil && time.Since(a.lastPaint) >= 50*time.Millisecond {
		invalidateRect.Call(a.window, 0, 0)
		a.lastPaint = time.Now()
	}
}

func (a *application) nextTranscription() {
	if a.closing || a.transcriptionBusy || len(a.transcriptionQueue) == 0 {
		return
	}
	path := a.transcriptionQueue[0]
	a.transcriptionQueue = a.transcriptionQueue[1:]
	a.transcriptionBusy = true
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		text, err := a.python.transcribe(a.transcriptionContext, path)
		select {
		case a.transcribed <- transcriptionResult{path, text, err}:
		case <-a.transcriptionContext.Done():
		}
	}()
}

func (a *application) clearDisplay() {
	a.displayText = ""
	a.displayQueue = nil
	a.displayUntil = time.Time{}
	a.readyUntil = time.Time{}
}

func (a *application) showOverlay(text string) {
	a.visibleText = text
	a.position()
	invalidateRect.Call(a.window, 0, 0)
	// Paint synchronously so a newly shown window never exposes its previous contents.
	user32.NewProc("UpdateWindow").Call(a.window)
	showWindow.Call(a.window, 4)
}

func (a *application) updateDisplay(now time.Time) {
	if a.closing || a.cancel != nil {
		return
	}
	if a.displayText != "" && !a.displayUntil.IsZero() && !now.Before(a.displayUntil) {
		a.displayText = ""
		a.displayUntil = time.Time{}
	}
	if a.displayText == "" && len(a.displayQueue) > 0 {
		a.displayText = a.displayQueue[0]
		a.displayQueue = a.displayQueue[1:]
		a.displayUntil = now.Add(time.Duration(a.config.Transcription.DisplaySeconds) * time.Second)
	}
	text := a.notificationText(now)
	if text == "" {
		a.visibleText = ""
		showWindow.Call(a.window, 0)
	} else if text != a.visibleText {
		a.showOverlay(text)
	}
}

func (a *application) notificationText(now time.Time) string {
	if a.displayText != "" {
		return a.displayText
	}
	if a.transcriptionBusy || len(a.transcriptionQueue) > 0 {
		return "Идёт транскрибация…"
	}
	if a.serviceLoading {
		return "Приложение загружается…"
	}
	if now.Before(a.readyUntil) {
		return "Приложение готово"
	}
	return ""
}

func (a *application) paint() {
	var p paintStruct
	dc, _, _ := beginPaint.Call(a.window, uintptr(unsafe.Pointer(&p)))
	defer endPaint.Call(a.window, uintptr(unsafe.Pointer(&p)))
	var r rect
	getClientRect.Call(a.window, uintptr(unsafe.Pointer(&r)))
	fill := func(box rect, color uintptr) {
		brush, _, _ := createBrush.Call(color)
		fillRect.Call(dc, uintptr(unsafe.Pointer(&box)), brush)
		deleteObject.Call(brush)
	}
	fill(r, 0x282020)
	setBkMode.Call(dc, 1)
	setTextColor.Call(dc, 0xf0f0f0)
	font, _, _ := getStockObject.Call(17)
	old, _, _ := selectObject.Call(dc, font)
	defer selectObject.Call(dc, old)
	if a.visibleText != "" && a.cancel == nil {
		text, _ := syscall.UTF16PtrFromString(a.visibleText)
		textRect := rect{12, 12, r.Right - 12, r.Bottom - 12}
		drawText.Call(dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&textRect)), 0x8810)
		return
	}
	label := "Opening microphone..."
	if a.recording.Load() {
		label = fmt.Sprintf("Recording  %.1fs", time.Since(a.started).Seconds())
	}
	text, _ := syscall.UTF16PtrFromString(label)
	textRect := rect{12, 8, r.Right - 12, r.Bottom - 24}
	drawText.Call(dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&textRect)), 0x25)
	bar := rect{16, r.Bottom - 18, r.Right - 16, r.Bottom - 10}
	fill(bar, 0x504040)
	level := math.Float64frombits(a.level.Load())
	bar.Right = bar.Left + int32(float64(bar.Right-bar.Left)*math.Sqrt(level))
	if bar.Right > bar.Left {
		fill(bar, 0x7070ff)
	}
}

func windowProcedure(window uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	a := currentApp
	if a.tray != nil && msg == a.tray.restartMessage {
		a.tray.added = false
		if err := a.tray.add(); err != nil {
			log.Print(err)
			a.lastTrayRetry = time.Now()
		}
		return 0
	}
	switch msg {
	case 0x111:
		if a.tray != nil && !a.closing {
			a.handleTrayCommand(wparam & 0xffff)
		}
		return 0 // WM_COMMAND
	case trayMessage:
		event := uint32(lparam & 0xffff)
		if a.tray != nil && !a.closing && (event == 0x7b || event == 0x400 || event == 0x401) {
			a.trayMenu()
		}
		return 0
	case 0x312:
		a.start()
		return 0 // WM_HOTKEY
	case 0x113:
		a.tick()
		return 0 // WM_TIMER
	case 0x0f:
		a.paint()
		return 0 // WM_PAINT
	case 0x14:
		return 1 // WM_ERASEBKGND
	case 0x21:
		return 3 // MA_NOACTIVATE
	case 0x84:
		return ^uintptr(0) // HTTRANSPARENT
	case 0x10:
		a.closing = true
		a.stopTranscription()
		a.transcriptionQueue = nil
		a.displayQueue = nil
		unregisterHotKey.Call(window, 1)
		if a.cancel != nil {
			a.cancel()
			showWindow.Call(window, 0)
		} else {
			destroyWindow.Call(window)
		}
		return 0
	case 2:
		postQuit.Call(0)
		return 0
	}
	result, _, _ := defWindowProc.Call(window, uintptr(msg), wparam, lparam)
	return result
}

func run(c Config) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Per-monitor DPI awareness is best effort on supported Windows versions.
	dpiProc := user32.NewProc("SetProcessDpiAwarenessContext")
	if dpiProc.Find() == nil {
		dpiProc.Call(^uintptr(3))
	}
	hotkey, _ := parseHotKey(c.HotKey)
	transcriptionContext, stopTranscription := context.WithCancel(context.Background())
	a := &application{config: c, hotkey: hotkey, result: make(chan recordingResult, 1), transcriptionContext: transcriptionContext, stopTranscription: stopTranscription, transcribed: make(chan transcriptionResult, 1)}
	defer func() { stopTranscription(); a.workers.Wait() }()
	currentApp = a
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	name, _ := syscall.UTF16PtrFromString(fmt.Sprintf("GoPushToTalkOverlay-%d", os.Getpid()))
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Procedure: syscall.NewCallback(windowProcedure), Instance: instance, ClassName: uintptr(unsafe.Pointer(name))}
	atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("register overlay class: %w", err)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	// TOPMOST | TOOLWINDOW | LAYERED | TRANSPARENT | NOACTIVATE.
	window, _, err := createWindow.Call(0x080800a8, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x80000000, 0, 0, uintptr(c.Overlay.Width), uintptr(c.Overlay.Height), 0, 0, instance, 0)
	if window == 0 {
		return fmt.Errorf("create overlay: %w", err)
	}
	a.window = window
	defer destroyWindow.Call(window)
	setLayered.Call(window, 0, uintptr(c.Overlay.Opacity), 2)
	ok, _, err := registerHotKey.Call(window, 1, uintptr(hotkey.Modifiers|0x4000), uintptr(hotkey.Key))
	if ok == 0 {
		return fmt.Errorf("register %s (it may be in use): %w", c.HotKey, err)
	}
	defer unregisterHotKey.Call(window, 1)
	timer, _, err := setTimer.Call(window, 1, 10, 0)
	if timer == 0 {
		return fmt.Errorf("create keyboard timer: %w", err)
	}
	defer killTimer.Call(window, timer)
	a.tray, err = newTray(window, c.HotKey)
	if err != nil {
		return err
	}
	defer a.tray.close()
	if c.Transcription.Enabled {
		a.python = newPythonService(transcriptionContext, c.Transcription)
		a.serviceStatus = make(chan serviceEvent, 1)
		a.serviceLoading = true
		a.updateDisplay(time.Now())
		a.workers.Add(1)
		go func() { defer a.workers.Done(); a.python.maintain(transcriptionContext, a.serviceStatus) }()
		defer func() { stopTranscription(); a.python.Close() }()
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-signals:
			postMessage.Call(window, 0x10, 0, 0)
		case <-finished:
		}
	}()
	log.Printf("Hold %s to record. Use the System Tray menu to exit. Output: %s", c.HotKey, c.OutputDirectory)
	var msg message
	for {
		result, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			if a.cancel != nil {
				a.cancel()
				<-a.result
			}
			return fmt.Errorf("read Windows messages: %w", err)
		}
		if result == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
