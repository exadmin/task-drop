package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// wavWriter writes the supplied format, including WAVEFORMATEXTENSIBLE when needed.
type wavWriter struct {
	file       *os.File
	size       uint32
	dataOffset int64
	factOffset int64
	blockSize  uint32
}

func newWAV(file *os.File, format []byte) (*wavWriter, error) {
	if len(format) < 16 || binary.LittleEndian.Uint16(format[12:]) == 0 {
		return nil, fmt.Errorf("invalid WAV format")
	}
	w := &wavWriter{file: file, blockSize: uint32(binary.LittleEndian.Uint16(format[12:]))}
	header := []byte("RIFF\x00\x00\x00\x00WAVEfmt \x00\x00\x00\x00")
	binary.LittleEndian.PutUint32(header[16:], uint32(len(format)))
	if _, err := file.Write(header); err != nil {
		return nil, err
	}
	if _, err := file.Write(format); err != nil {
		return nil, err
	}
	if len(format)%2 != 0 {
		if _, err := file.Write([]byte{0}); err != nil {
			return nil, err
		}
	}
	if binary.LittleEndian.Uint16(format) != 1 {
		if _, err := file.Write([]byte("fact\x04\x00\x00\x00\x00\x00\x00\x00")); err != nil {
			return nil, err
		}
		position, err := file.Seek(0, 1)
		if err != nil {
			return nil, err
		}
		w.factOffset = position - 4
	}
	if _, err := file.Write([]byte("data\x00\x00\x00\x00")); err != nil {
		return nil, err
	}
	var err error
	w.dataOffset, err = file.Seek(0, 1)
	return w, err
}

func (w *wavWriter) Write(data []byte) error {
	if uint64(w.size)+uint64(len(data))+uint64(w.dataOffset) > 0xffffffff {
		return fmt.Errorf("recording exceeds the WAV size limit")
	}
	n, err := w.file.Write(data)
	w.size += uint32(n)
	return err
}

func (w *wavWriter) Close() (err error) {
	defer func() { err = errors.Join(err, w.file.Close()) }()
	if w.size%2 != 0 {
		if _, err := w.file.Write([]byte{0}); err != nil {
			return err
		}
	}
	end, err := w.file.Seek(0, 1)
	if err != nil {
		return err
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(end-8))
	if _, err = w.file.WriteAt(b[:], 4); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(b[:], w.size)
	if _, err = w.file.WriteAt(b[:], w.dataOffset-4); err != nil {
		return err
	}
	if w.factOffset != 0 {
		binary.LittleEndian.PutUint32(b[:], w.size/w.blockSize)
		if _, err = w.file.WriteAt(b[:], w.factOffset); err != nil {
			return err
		}
	}
	return w.file.Sync()
}
