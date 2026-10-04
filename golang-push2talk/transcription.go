package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type transcriptionResult struct {
	path, text string
	err        error
}
type tailWriter struct{ data []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	const limit = 8192
	n := len(p)
	if n >= limit {
		w.data = append(w.data[:0], p[n-limit:]...)
	} else {
		if len(w.data)+n > limit {
			w.data = w.data[len(w.data)+n-limit:]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}

func transcribeWAV(ctx context.Context, c TranscriptionConfig, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	f, err := os.CreateTemp("", "push2talk-transcript-*.json")
	if err != nil {
		return "", err
	}
	resultPath := f.Name()
	f.Close()
	defer os.Remove(resultPath)
	textPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".txt"
	command := exec.CommandContext(ctx, c.PythonExecutable, c.LauncherPath, path, "--model", c.Model, "--device", c.Device, "--json-output", resultPath, "--output", textPath)
	configureChildProcess(command)
	command.Cancel = func() error { return stopChildProcess(command.Process) }
	command.WaitDelay = 5 * time.Second
	var diagnostics tailWriter
	command.Stdout = io.Discard
	command.Stderr = &diagnostics
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("transcription: %w", ctx.Err())
		}
		return "", fmt.Errorf("transcription failed: %w\n%s", err, strings.TrimSpace(string(diagnostics.data)))
	}
	f, err = os.Open(resultPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var result struct {
		Text string `json:"text"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 16*1024*1024)).Decode(&result); err != nil {
		return "", fmt.Errorf("read Python transcription: %w", err)
	}
	if !utf8.ValidString(result.Text) {
		return "", fmt.Errorf("Python transcription is not UTF-8")
	}
	text := strings.TrimSpace(strings.ReplaceAll(result.Text, "\x00", ""))
	if err = saveTranscript(path, c.StoreTextPath, text); err != nil {
		return "", err
	}
	return text, nil
}

// Keep the recording until every transcript destination has been saved successfully.
func saveTranscript(wavPath, extraDirectory, text string) error {
	textPath := strings.TrimSuffix(wavPath, filepath.Ext(wavPath)) + ".txt"
	data := []byte(text + "\n")
	if err := os.WriteFile(textPath, data, 0600); err != nil {
		return fmt.Errorf("save transcript %s: %w", textPath, err)
	}
	if extraDirectory != "" {
		if err := os.MkdirAll(extraDirectory, 0755); err != nil {
			return fmt.Errorf("create transcript directory: %w", err)
		}
		extraPath := filepath.Join(extraDirectory, filepath.Base(textPath))
		if err := os.WriteFile(extraPath, data, 0600); err != nil {
			return fmt.Errorf("save additional transcript %s: %w", extraPath, err)
		}
	}
	if err := os.Remove(wavPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete transcribed WAV %s: %w", wavPath, err)
	}
	return nil
}
