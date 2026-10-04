package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	PathToStoreText    string              `json:"path-to-store-text"`
	Transcription      TranscriptionConfig `json:"transcription"`
	OutputSampleRate   int                 `json:"output_sample_rate"`
	HideConsole        bool                `json:"hide_console"`
	LogFile            string              `json:"log_file"`
	HotKey             string              `json:"hot_key"`
	OutputDirectory    string              `json:"output_directory"`
	MicrophoneDeviceID string              `json:"microphone_device_id"`
	AudioPollMS        int                 `json:"audio_poll_ms"`
	Overlay            struct {
		Width        int `json:"width"`
		Height       int `json:"height"`
		BottomMargin int `json:"bottom_margin"`
		Opacity      int `json:"opacity"`
	} `json:"overlay"`
}

type HotKey struct {
	Modifiers uint32
	Key       uint32
}

type TranscriptionConfig struct {
	StoreTextPath    string `json:"-"`
	Enabled          bool   `json:"enabled"`
	PythonExecutable string `json:"python_executable"`
	LauncherPath     string `json:"launcher_path"`
	Model            string `json:"model"`
	Device           string `json:"device"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	DisplaySeconds   int    `json:"display_seconds"`
	Width            int    `json:"width"`
	MaxHeight        int    `json:"max_height"`
}

func defaultTranscription() TranscriptionConfig {
	return TranscriptionConfig{Enabled: true, PythonExecutable: "python", LauncherPath: "../python-tst/transcribe.py", Model: "v3_e2e_rnnt", Device: "auto", TimeoutSeconds: 1200, DisplaySeconds: 5, Width: 640, MaxHeight: 320}
}

func parseHotKey(s string) (HotKey, error) {
	var h HotKey
	for _, part := range strings.Split(strings.ToUpper(s), "+") {
		part = strings.TrimSpace(part)
		var mod uint32
		switch part {
		case "CTRL", "CONTROL":
			mod = 2
		case "ALT":
			mod = 1
		case "SHIFT":
			mod = 4
		case "WIN":
			mod = 8
		default:
			if h.Key != 0 {
				return h, fmt.Errorf("hot_key must contain one key")
			}
			if len(part) == 1 && ((part[0] >= 'A' && part[0] <= 'Z') || (part[0] >= '0' && part[0] <= '9')) {
				h.Key = uint32(part[0])
			} else {
				return h, fmt.Errorf("unsupported hot_key key %q; use A-Z or 0-9", part)
			}
		}
		if mod != 0 {
			if h.Modifiers&mod != 0 {
				return h, fmt.Errorf("duplicate hot_key modifier %q", part)
			}
			h.Modifiers |= mod
		}
	}
	if h.Key == 0 || h.Modifiers == 0 {
		return h, fmt.Errorf("hot_key requires modifiers and one key")
	}
	return h, nil
}

func loadConfig(path string) (Config, error) {
	c := Config{HideConsole: true, LogFile: "push2talk.log", OutputSampleRate: 16000, Transcription: defaultTranscription()}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("properties.json must contain one JSON object")
	}
	if _, err = parseHotKey(c.HotKey); err != nil {
		return c, err
	}
	if c.OutputDirectory == "" || c.AudioPollMS < 1 || c.AudioPollMS > 100 || c.Overlay.Width < 160 || c.Overlay.Width > 1000 || c.Overlay.Height < 50 || c.Overlay.Height > 300 || c.Overlay.BottomMargin < 0 || c.Overlay.BottomMargin > 1000 || c.Overlay.Opacity < 1 || c.Overlay.Opacity > 255 {
		return c, fmt.Errorf("invalid output directory, polling interval, or overlay settings")
	}
	if !filepath.IsAbs(c.OutputDirectory) {
		c.OutputDirectory = filepath.Join(filepath.Dir(path), c.OutputDirectory)
	}
	if c.LogFile == "" {
		return c, fmt.Errorf("log_file must not be empty")
	}
	if c.OutputSampleRate < 8000 || c.OutputSampleRate > 48000 {
		return c, fmt.Errorf("output_sample_rate must be between 8000 and 48000")
	}
	if !filepath.IsAbs(c.LogFile) {
		c.LogFile = filepath.Join(filepath.Dir(path), c.LogFile)
	}
	t := &c.Transcription
	if strings.TrimSpace(c.PathToStoreText) == "" {
		c.PathToStoreText = ""
	} else {
		c.PathToStoreText = filepath.FromSlash(strings.ReplaceAll(c.PathToStoreText, "\\", "/"))
		if !filepath.IsAbs(c.PathToStoreText) {
			c.PathToStoreText = filepath.Join(filepath.Dir(path), c.PathToStoreText)
		}
	}
	t.StoreTextPath = c.PathToStoreText
	if t.Enabled {
		if c.OutputSampleRate != 16000 {
			return c, fmt.Errorf("transcription requires output_sample_rate 16000")
		}
		if t.PythonExecutable == "" || t.LauncherPath == "" || t.Model == "" || (t.Device != "auto" && t.Device != "cpu" && t.Device != "cuda") || t.TimeoutSeconds < 1 || t.TimeoutSeconds > 86400 || t.DisplaySeconds < 1 || t.DisplaySeconds > 60 || t.Width < 280 || t.Width > 1600 || t.MaxHeight < 80 || t.MaxHeight > 1000 {
			return c, fmt.Errorf("invalid transcription settings")
		}
		if !filepath.IsAbs(t.LauncherPath) {
			t.LauncherPath = filepath.Join(filepath.Dir(path), t.LauncherPath)
		}
		if strings.ContainsAny(t.PythonExecutable, "/\\") && !filepath.IsAbs(t.PythonExecutable) {
			t.PythonExecutable = filepath.Join(filepath.Dir(path), t.PythonExecutable)
		}
	}
	return c, nil
}
