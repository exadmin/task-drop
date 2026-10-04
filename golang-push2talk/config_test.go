package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguration(t *testing.T) {
	c, err := loadConfig("properties.json.example")
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseHotKey(c.HotKey)
	if err != nil || h.Modifiers != 3 || h.Key != 'P' {
		t.Fatalf("unexpected hotkey: %+v, %v", h, err)
	}
	for _, s := range []string{"P", "Ctrl+Alt", "Ctrl+Ctrl+P", "Ctrl+P+Q", "Ctrl++P", "Ctrl+F1"} {
		if _, err := parseHotKey(s); err == nil {
			t.Errorf("accepted invalid hotkey %q", s)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "properties.json")
	bytes, err := os.ReadFile("properties.json.example")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	c, err = loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.OutputDirectory != filepath.Join(dir, "recordings") {
		t.Fatalf("output directory: %s", c.OutputDirectory)
	}
	if c.LogFile != filepath.Join(dir, "push2talk.log") || !c.HideConsole {
		t.Fatalf("unexpected background settings: %+v", c)
	}
	for _, input := range []string{`{"unknown":true}`, string(bytes) + ` {}`, `null`} {
		if c.OutputSampleRate != 16000 {
			t.Fatalf("unexpected output sample rate: %d", c.OutputSampleRate)
		}
		if err = os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = loadConfig(path); err == nil {
			t.Errorf("accepted invalid configuration %s", input)
		}
	}
}
