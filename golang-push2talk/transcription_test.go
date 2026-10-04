package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pythonForTest(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python is required for subprocess integration tests")
	}
	return python
}

func TestSaveTranscriptBeforeDeletingRecording(t *testing.T) {
	for _, failure := range []string{"", "primary", "additional"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			wav := filepath.Join(dir, "speech.wav")
			primary := filepath.Join(dir, "speech.txt")
			extra := filepath.Join(dir, "texts")
			if err := os.WriteFile(wav, []byte("audio"), 0600); err != nil {
				t.Fatal(err)
			}
			if failure == "primary" {
				if err := os.Mkdir(primary, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "additional" {
				if err := os.WriteFile(extra, []byte("blocks directory creation"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := saveTranscript(wav, extra, "Распознанный текст")
			if failure != "" {
				if err == nil {
					t.Fatal("expected a transcript saving error")
				}
				data, readErr := os.ReadFile(wav)
				if readErr != nil || string(data) != "audio" {
					t.Fatalf("recording was lost after saving failed: %v", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{primary, filepath.Join(extra, "speech.txt")} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "Распознанный текст\n" {
					t.Fatalf("invalid transcript at %s: %q, %v", path, data, err)
				}
			}
			if _, err := os.Stat(wav); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recording was not deleted: %v", err)
			}
		})
	}
}

func TestAdditionalTranscriptDirectoryConfig(t *testing.T) {
	data, err := os.ReadFile("properties.json.example")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, value := range []string{"missing", "", "   ", "texts/nested", `texts\nested`, filepath.Join(dir, "absolute")} {
		t.Run(value, func(t *testing.T) {
			delete(config, "path-to-store-text")
			if value != "missing" {
				config["path-to-store-text"] = value
			}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "properties.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			c, err := loadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := ""
			if value == "texts/nested" || value == `texts\nested` {
				expected = filepath.Join(dir, "texts", "nested")
			} else if filepath.IsAbs(value) {
				expected = filepath.Join(dir, "absolute")
			}
			if c.PathToStoreText != expected || c.Transcription.StoreTextPath != expected {
				t.Fatalf("expected %q, got %q and %q", expected, c.PathToStoreText, c.Transcription.StoreTextPath)
			}
		})
	}
}

func TestSaveTranscriptWithoutAdditionalCopy(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "speech.wav")
	if err := os.WriteFile(wav, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveTranscript(wav, "", "Текст"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "speech.txt"))
	if err != nil || string(data) != "Текст\n" {
		t.Fatalf("invalid primary transcript: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected only the primary TXT: %v, %v", entries, err)
	}
}

func TestTranscriptionArgumentsAndUnicode(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "launcher with spaces.py")
	code := `import json, pathlib, sys
args=sys.argv[1:]
text="Результат: " + pathlib.Path(args[0]).name
pathlib.Path(args[args.index("--json-output")+1]).write_text(json.dumps({"text":text},ensure_ascii=False),encoding="utf-8")
pathlib.Path(args[args.index("--output")+1]).write_text(text,encoding="utf-8")
print("progress does not belong in the transcript")
`
	if err := os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c := defaultTranscription()
	c.PythonExecutable = pythonForTest(t)
	c.LauncherPath = script
	path := filepath.Join(directory, "recording & 100% with spaces.wav")
	text, err := transcribeWAV(context.Background(), c, path)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Результат: "+filepath.Base(path) {
		t.Fatalf("unexpected transcript: %q", text)
	}
	if _, err = os.Stat(strings.TrimSuffix(path, ".wav") + ".txt"); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptionFailureAndTimeout(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "fail.py")
	c := defaultTranscription()
	c.PythonExecutable = pythonForTest(t)
	c.LauncherPath = script
	if err := os.WriteFile(script, []byte("import sys\nprint('specific failure',file=sys.stderr)\nsys.exit(3)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := transcribeWAV(context.Background(), c, "input.wav")
	if err == nil || !strings.Contains(err.Error(), "specific failure") {
		t.Fatalf("missing diagnostic: %v", err)
	}
	if err = os.WriteFile(script, []byte("import time\ntime.sleep(30)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.TimeoutSeconds = 1
	_, err = transcribeWAV(context.Background(), c, "input.wav")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout result: %v", err)
	}
}

func TestRealGigaAMBridge(t *testing.T) {
	if os.Getenv("P2T_INFERENCE_TEST") != "1" {
		t.Skip("set P2T_INFERENCE_TEST=1 to run cached-model inference")
	}
	sample := filepath.Join("..", "python-tst", "test-output", "example.wav")
	data, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "speech sample.wav")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	c := defaultTranscription()
	c.PythonExecutable = pythonForTest(t)
	c.Device = "cpu"
	c.LauncherPath, err = filepath.Abs(filepath.Join("..", "python-tst", "transcribe.py"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := transcribeWAV(context.Background(), c, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(text), "лукоморья") {
		t.Fatalf("unexpected recognition: %q", text)
	}
	t.Logf("Recognized %d characters", len([]rune(text)))
}
