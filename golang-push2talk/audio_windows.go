//go:build windows && (amd64 || arm64)

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var ole32 = syscall.NewLazyDLL("ole32.dll")
var coInitialize = ole32.NewProc("CoInitializeEx")
var coUninitialize = ole32.NewProc("CoUninitialize")
var coCreate = ole32.NewProc("CoCreateInstance")
var coFree = ole32.NewProc("CoTaskMemFree")

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var clsidEnumerator = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
var iidEnumerator = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
var iidAudio = guid{0x1cb9ad4c, 0xdbfa, 0x4c32, [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
var iidCapture = guid{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}

func hresult(value uintptr) error {
	if int32(value) < 0 {
		return fmt.Errorf("Windows audio error 0x%08X", uint32(value))
	}
	return nil
}

type comObject struct{ table *[15]uintptr }

//go:uintptrescapes
func comCall(object *comObject, slot int, args ...uintptr) error {
	method := object.table[slot]
	result, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)...)
	runtime.KeepAlive(object)
	return hresult(result)
}

type audioSession struct {
	enumerator, device, client, capture *comObject
	format                              []byte
	blockSize                           int
	started                             bool
	converter                           *pcmConverter
}

func openAudio(c Config) (*audioSession, error) {
	a := &audioSession{}
	fail := func(err error) (*audioSession, error) { a.close(); return nil, err }
	hr, _, _ := coCreate.Call(uintptr(unsafe.Pointer(&clsidEnumerator)), 0, 1, uintptr(unsafe.Pointer(&iidEnumerator)), uintptr(unsafe.Pointer(&a.enumerator)))
	if err := hresult(hr); err != nil {
		return fail(err)
	}
	if c.MicrophoneDeviceID == "" {
		// eCapture = 1; eCommunications = 2.
		if err := comCall(a.enumerator, 4, 1, 2, uintptr(unsafe.Pointer(&a.device))); err != nil {
			return fail(err)
		}
	} else {
		id, err := syscall.UTF16PtrFromString(c.MicrophoneDeviceID)
		if err != nil {
			return fail(err)
		}
		err = comCall(a.enumerator, 5, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&a.device)))
		runtime.KeepAlive(id)
		if err != nil {
			return fail(err)
		}
	}
	if err := comCall(a.device, 3, uintptr(unsafe.Pointer(&iidAudio)), 1, 0, uintptr(unsafe.Pointer(&a.client))); err != nil {
		return fail(err)
	}
	var format unsafe.Pointer
	if err := comCall(a.client, 8, uintptr(unsafe.Pointer(&format))); err != nil {
		return fail(err)
	}
	defer coFree.Call(uintptr(format))
	base := unsafe.Slice((*byte)(format), 18)
	size := 18 + int(binary.LittleEndian.Uint16(base[16:]))
	a.format = append([]byte(nil), unsafe.Slice((*byte)(format), size)...)
	a.blockSize = int(binary.LittleEndian.Uint16(a.format[12:]))
	if a.blockSize == 0 {
		return fail(fmt.Errorf("microphone reports an invalid frame size"))
	}
	var err error
	a.converter, err = newPCMConverter(a.format, c.OutputSampleRate)
	if err != nil {
		return fail(err)
	}
	// Shared mode, no exclusive fallback. A 100 ms buffer is drained frequently.
	if err := comCall(a.client, 3, 0, 0, 1000000, 0, uintptr(format), 0); err != nil {
		return fail(err)
	}
	if err := comCall(a.client, 14, uintptr(unsafe.Pointer(&iidCapture)), uintptr(unsafe.Pointer(&a.capture))); err != nil {
		return fail(err)
	}
	return a, nil
}

func (a *audioSession) close() {
	if a.started {
		_ = comCall(a.client, 11)
		a.started = false
	}
	for _, object := range []*comObject{a.capture, a.client, a.device, a.enumerator} {
		if object != nil {
			_ = comCall(object, 2)
		}
	}
}

func audioLevel(data, format []byte) float64 {
	tag := binary.LittleEndian.Uint16(format)
	bits := int(binary.LittleEndian.Uint16(format[14:]))
	if tag == 0xfffe && len(format) >= 40 {
		tag = uint16(binary.LittleEndian.Uint32(format[24:]))
	}
	step := bits / 8
	if step == 0 {
		return 0
	}
	var peak float64
	for i := 0; i+step <= len(data); i += step {
		var value float64
		switch {
		case tag == 3 && bits == 32:
			value = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i:])))
		case tag == 3 && bits == 64:
			value = math.Float64frombits(binary.LittleEndian.Uint64(data[i:]))
		case tag == 1 && bits == 8:
			value = float64(int(data[i])-128) / 128
		case tag == 1 && bits == 16:
			value = float64(int16(binary.LittleEndian.Uint16(data[i:]))) / 32768
		case tag == 1 && bits == 24:
			value = float64(int32(uint32(data[i])<<8|uint32(data[i+1])<<16|uint32(data[i+2])<<24)) / 2147483648
		case tag == 1 && bits == 32:
			value = float64(int32(binary.LittleEndian.Uint32(data[i:]))) / 2147483648
		}
		value = math.Abs(value)
		if value > peak {
			peak = value
		}
	}
	return math.Min(peak, 1)
}

func (a *audioSession) drain(w *wavWriter, level *atomic.Uint64) error {
	for {
		var count uint32
		if err := comCall(a.capture, 5, uintptr(unsafe.Pointer(&count))); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		var data unsafe.Pointer
		var frames, flags uint32
		if err := comCall(a.capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0); err != nil {
			return err
		}
		bytes := make([]byte, int(frames)*a.blockSize)
		if flags&2 == 0 && data != nil {
			copy(bytes, unsafe.Slice((*byte)(data), len(bytes)))
		} else {
			tag := binary.LittleEndian.Uint16(a.format)
			if tag == 0xfffe && len(a.format) >= 40 {
				tag = uint16(binary.LittleEndian.Uint32(a.format[24:]))
			}
			if tag == 1 && binary.LittleEndian.Uint16(a.format[14:]) == 8 {
				for i := range bytes {
					bytes[i] = 128
				}
			}
		}
		err := comCall(a.capture, 4, uintptr(frames))
		if err != nil {
			return err
		}
		converted, err := a.converter.push(bytes, flags&2 != 0)
		if err != nil {
			return err
		}
		level.Store(math.Float64bits(audioLevel(converted, pcmFormat(a.converter.targetRate))))
		if err = w.Write(converted); err != nil {
			return err
		}
	}
}

func record(ctx context.Context, c Config, level *atomic.Uint64, ready func()) (path string, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitialize.Call(0, 0)
	if err = hresult(hr); err != nil {
		return "", err
	}
	defer coUninitialize.Call()
	if ctx.Err() != nil {
		return "", nil
	}
	a, err := openAudio(c)
	if err != nil {
		return "", err
	}
	defer a.close()
	if ctx.Err() != nil {
		return "", nil
	}
	if err = os.MkdirAll(c.OutputDirectory, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(c.OutputDirectory, ".recording-*.tmp")
	if err != nil {
		return "", err
	}
	temp := f.Name()
	defer os.Remove(temp)
	w, err := newWAV(f, pcmFormat(c.OutputSampleRate))
	if err != nil {
		f.Close()
		return "", err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, w.Close())
		}
	}()
	if ctx.Err() != nil {
		return "", nil
	}
	if err = comCall(a.client, 10); err != nil {
		return "", err
	}
	a.started = true
	ready()
	ticker := time.NewTicker(time.Duration(c.AudioPollMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			err = comCall(a.client, 11)
			a.started = false
			if err == nil {
				err = a.drain(w, level)
			}
			if err == nil {
				err = w.Write(a.converter.flush())
			}
			err = errors.Join(err, w.Close())
			closed = true
			if err != nil {
				return "", err
			}
			if w.size == 0 {
				return "", nil
			}
			path = filepath.Join(c.OutputDirectory, "recording-"+time.Now().Format("20060102-150405.000000000")+".wav")
			err = os.Rename(temp, path)
			return path, err
		case <-ticker.C:
			if err = a.drain(w, level); err != nil {
				return "", err
			}
		}
	}
}
