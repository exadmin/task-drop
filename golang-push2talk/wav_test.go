package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWAVChunks(t *testing.T) {
	for _, tag := range []uint16{1, 3, 0xfffe} {
		t.Run(string(rune(tag)), func(t *testing.T) {
			format := make([]byte, 18)
			binary.LittleEndian.PutUint16(format, tag)
			binary.LittleEndian.PutUint16(format[2:], 1)
			binary.LittleEndian.PutUint32(format[4:], 48000)
			binary.LittleEndian.PutUint32(format[8:], 48000)
			binary.LittleEndian.PutUint16(format[12:], 1)
			binary.LittleEndian.PutUint16(format[14:], 8)
			if tag == 0xfffe {
				format = append(format, make([]byte, 22)...)
				binary.LittleEndian.PutUint16(format[16:], 22)
			}
			path := filepath.Join(t.TempDir(), "test.wav")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			w, err := newWAV(f, format)
			if err != nil {
				f.Close()
				t.Fatal(err)
			}
			payload := []byte{1, 2, 3}
			if err = w.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || int(binary.LittleEndian.Uint32(data[4:])) != len(data)-8 {
				t.Fatal("invalid RIFF header")
			}
			seenData, seenFact := false, false
			for offset := 12; offset+8 <= len(data); {
				size := int(binary.LittleEndian.Uint32(data[offset+4:]))
				start := offset + 8
				if start+size > len(data) {
					t.Fatal("chunk exceeds file")
				}
				switch string(data[offset : offset+4]) {
				case "fmt ":
					if !bytes.Equal(data[start:start+size], format) {
						t.Fatal("device format changed")
					}
				case "fact":
					seenFact = true
					if binary.LittleEndian.Uint32(data[start:]) != 3 {
						t.Fatal("invalid sample count")
					}
				case "data":
					seenData = true
					if !bytes.Equal(data[start:start+size], payload) {
						t.Fatal("audio payload changed")
					}
				}
				offset = start + size + size%2
			}
			if !seenData || seenFact != (tag != 1) {
				t.Fatal("missing or unexpected WAV chunks")
			}
		})
	}
}
