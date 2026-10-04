package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func floatStereoFormat(rate int) []byte {
	f := pcmFormat(rate)
	binary.LittleEndian.PutUint16(f, 3)
	binary.LittleEndian.PutUint16(f[2:], 2)
	binary.LittleEndian.PutUint16(f[12:], 8)
	binary.LittleEndian.PutUint16(f[14:], 32)
	return f
}

func convertTone(t *testing.T, frequency float64, packets int) []byte {
	t.Helper()
	f := floatStereoFormat(48000)
	data := make([]byte, 48000*8)
	for frame := 0; frame < 48000; frame++ {
		value := float32(0.5 * math.Sin(2*math.Pi*frequency*float64(frame)/48000))
		binary.LittleEndian.PutUint32(data[frame*8:], math.Float32bits(value))
		binary.LittleEndian.PutUint32(data[frame*8+4:], math.Float32bits(value))
	}
	c, err := newPCMConverter(f, 16000)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	size := 48000 / packets * 8
	for start := 0; start < len(data); start += size {
		b, err := c.push(data[start:min(start+size, len(data))], false)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return append(out, c.flush()...)
}

func TestStreamingResampler(t *testing.T) {
	whole := convertTone(t, 1000, 1)
	packets := convertTone(t, 1000, 100)
	if len(whole) != 16000*2 || !bytes.Equal(whole, packets) {
		t.Fatal("packet boundaries changed samples or duration")
	}
	rms := func(data []byte) float64 {
		var sum float64
		for i := 400; i < len(data)-400; i += 2 {
			value := float64(int16(binary.LittleEndian.Uint16(data[i:]))) / 32768
			sum += value * value
		}
		return math.Sqrt(sum / float64((len(data)-800)/2))
	}
	pass, stop := rms(whole), rms(convertTone(t, 12000, 100))
	if math.Abs(pass-0.5/math.Sqrt2) > 0.01 {
		t.Fatalf("speech-band tone amplitude changed: %f", pass)
	}
	if stop > 0.003 {
		t.Fatalf("alias rejection is too weak: %f", stop)
	}
}

func TestPCMConversion(t *testing.T) {
	c, err := newPCMConverter(floatStereoFormat(16000), 16000)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint32(data, math.Float32bits(0.75))
	binary.LittleEndian.PutUint32(data[4:], math.Float32bits(0.25))
	binary.LittleEndian.PutUint32(data[8:], math.Float32bits(float32(math.NaN())))
	b, err := c.push(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if int16(binary.LittleEndian.Uint16(b)) != 16384 || int16(binary.LittleEndian.Uint16(b[2:])) != 0 {
		t.Fatal("stereo mix or nonfinite sanitization failed")
	}
	silent, err := c.push(data, true)
	if err != nil || !bytes.Equal(silent, make([]byte, 4)) {
		t.Fatal("silence was not zero PCM")
	}
}

func TestResamplerFractionalRatesAndFlush(t *testing.T) {
	for _, rate := range []int{8000, 16000, 44100, 48000, 96000} {
		c, err := newPCMConverter(pcmFormat(rate), 16000)
		if err != nil {
			t.Fatal(err)
		}
		frames := 997
		data := make([]byte, frames*2)
		for i := 0; i < frames; i++ {
			binary.LittleEndian.PutUint16(data[i*2:], 8192)
		}
		b, err := c.push(data, false)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, c.flush()...)
		expected := (frames*16000 + rate - 1) / rate
		if len(b) != expected*2 {
			t.Fatalf("rate %d: got %d samples, expected %d", rate, len(b)/2, expected)
		}
		for i := 0; i < len(b); i += 2 {
			if int16(binary.LittleEndian.Uint16(b[i:])) != 8192 {
				t.Fatalf("rate %d: DC or endpoint changed", rate)
			}
		}
		if len(c.flush()) != 0 {
			t.Fatal("flush duplicated samples")
		}
	}
}
