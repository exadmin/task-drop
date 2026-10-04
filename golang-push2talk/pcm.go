package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

func pcmFormat(sampleRate int) []byte {
	f := make([]byte, 16)
	binary.LittleEndian.PutUint16(f, 1)
	binary.LittleEndian.PutUint16(f[2:], 1)
	binary.LittleEndian.PutUint32(f[4:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(f[8:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(f[12:], 2)
	binary.LittleEndian.PutUint16(f[14:], 16)
	return f
}

// pcmConverter retains filter history across WASAPI packets. Downsampling uses
// a windowed sinc low-pass filter rather than discarding high-frequency samples.
type pcmConverter struct {
	tag                                                   uint16
	bits, channels, block, sourceRate, targetRate, radius int
	samples                                               []float64
	base, total, next                                     int64
	first, last                                           float64
}

func newPCMConverter(format []byte, targetRate int) (*pcmConverter, error) {
	if len(format) < 16 {
		return nil, fmt.Errorf("microphone format is truncated")
	}
	c := &pcmConverter{tag: binary.LittleEndian.Uint16(format), channels: int(binary.LittleEndian.Uint16(format[2:])), sourceRate: int(binary.LittleEndian.Uint32(format[4:])), block: int(binary.LittleEndian.Uint16(format[12:])), bits: int(binary.LittleEndian.Uint16(format[14:])), targetRate: targetRate}
	if c.tag == 0xfffe {
		tail := []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
		if len(format) < 40 || !bytes.Equal(format[28:40], tail) {
			return nil, fmt.Errorf("unsupported extensible microphone format")
		}
		sub := binary.LittleEndian.Uint32(format[24:])
		if sub != 1 && sub != 3 {
			return nil, fmt.Errorf("unsupported microphone subtype %d", sub)
		}
		c.tag = uint16(sub)
	}
	supported := c.tag == 1 && (c.bits == 8 || c.bits == 16 || c.bits == 24 || c.bits == 32) || c.tag == 3 && (c.bits == 32 || c.bits == 64)
	if !supported || c.channels < 1 || c.channels > 32 || c.block != c.channels*(c.bits/8) || c.sourceRate < 8000 || c.sourceRate > 384000 || targetRate < 8000 || targetRate > 48000 {
		return nil, fmt.Errorf("unsupported microphone audio format")
	}
	c.radius = 32 * max(1, (c.sourceRate+targetRate-1)/targetRate)
	return c, nil
}

func (c *pcmConverter) decode(b []byte) float64 {
	var value float64
	switch {
	case c.tag == 3 && c.bits == 32:
		value = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case c.tag == 3 && c.bits == 64:
		value = math.Float64frombits(binary.LittleEndian.Uint64(b))
	case c.bits == 8:
		value = float64(int(b[0])-128) / 128
	case c.bits == 16:
		value = float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case c.bits == 24:
		value = float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)) / 2147483648
	case c.bits == 32:
		value = float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return max(-1, min(1, value))
}

func appendPCM(out []byte, value float64) []byte {
	sample := int16(max(-32768, min(32767, math.Round(value*32768))))
	return binary.LittleEndian.AppendUint16(out, uint16(sample))
}

func (c *pcmConverter) push(data []byte, silent bool) ([]byte, error) {
	if len(data)%c.block != 0 {
		return nil, fmt.Errorf("audio packet contains an incomplete frame")
	}
	for offset := 0; offset < len(data); offset += c.block {
		value := 0.0
		if !silent {
			for channel := 0; channel < c.channels; channel++ {
				value += c.decode(data[offset+channel*c.bits/8:])
			}
			value /= float64(c.channels)
		}
		if c.total == 0 {
			c.first = value
		}
		c.last = value
		c.samples = append(c.samples, value)
		c.total++
	}
	return c.produce(false), nil
}

func (c *pcmConverter) produce(final bool) []byte {
	var out []byte
	for c.next*int64(c.sourceRate) < c.total*int64(c.targetRate) {
		position := float64(c.next*int64(c.sourceRate)) / float64(c.targetRate)
		center := int64(math.Floor(position))
		if c.sourceRate == c.targetRate {
			out = appendPCM(out, c.samples[center-c.base])
			c.next++
			continue
		}
		if !final && center+int64(c.radius) >= c.total {
			break
		}
		cutoff := math.Min(1, float64(c.targetRate)/float64(c.sourceRate)) * 0.94
		sum, weights := 0.0, 0.0
		for index := center - int64(c.radius) + 1; index <= center+int64(c.radius); index++ {
			distance := position - float64(index)
			if math.Abs(distance) >= float64(c.radius) {
				continue
			}
			x := math.Pi * distance * cutoff
			weight := cutoff
			if math.Abs(x) > 1e-12 {
				weight *= math.Sin(x) / x
			}
			weight *= 0.5 + 0.5*math.Cos(math.Pi*distance/float64(c.radius))
			value := c.first
			if index >= c.total {
				value = c.last
			} else if index >= 0 {
				value = c.samples[index-c.base]
			}
			sum += value * weight
			weights += weight
		}
		out = appendPCM(out, sum/weights)
		c.next++
	}
	keepFrom := max(c.base, c.next*int64(c.sourceRate)/int64(c.targetRate)-int64(c.radius))
	keepFrom = min(keepFrom, c.total)
	drop := int(keepFrom - c.base)
	if drop > 0 {
		copy(c.samples, c.samples[drop:])
		c.samples = c.samples[:len(c.samples)-drop]
		c.base = keepFrom
	}
	return out
}

func (c *pcmConverter) flush() []byte { return c.produce(true) }
