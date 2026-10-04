package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResidentServiceAndRestart(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "service.py")
	code := `import json,os,sys
print(json.dumps({"type":"ready"}),flush=True)
for line in sys.stdin:
 r=json.loads(line)
 if r["wav_files"]==["bad.wav"]:
  print(json.dumps({"type":"error","id":r["id"],"error":"bad WAV"}),flush=True)
 else:
  print(json.dumps({"type":"result","id":r["id"],"text":"Текст "+str(os.getpid())},ensure_ascii=False),flush=True)
`
	if err := os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c := defaultTranscription()
	c.PythonExecutable = pythonForTest(t)
	c.LauncherPath = script
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newPythonService(ctx, c)
	defer s.Close()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := s.transcribe(ctx, filepath.Join(dir, "one.wav"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.transcribe(ctx, filepath.Join(dir, "two.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("Python restarted between requests")
	}
	if _, err = s.transcribe(ctx, "bad.wav"); err == nil || !strings.Contains(err.Error(), "bad WAV") {
		t.Fatalf("missing request error: %v", err)
	}
	third, err := s.transcribe(ctx, filepath.Join(dir, "three.wav"))
	if err != nil || third != first {
		t.Fatalf("request error stopped resident service: %v", err)
	}
	if err = stopChildProcess(s.command.Process); err != nil {
		t.Fatal(err)
	}
	<-s.done
	fourth, err := s.transcribe(ctx, filepath.Join(dir, "four.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if fourth == first {
		t.Fatal("service did not restart after termination")
	}
}

func TestRealResidentGigaAM(t *testing.T) {
	if os.Getenv("P2T_INFERENCE_TEST") != "1" {
		t.Skip("set P2T_INFERENCE_TEST=1 for resident model inference")
	}
	data, err := os.ReadFile(filepath.Join("..", "python-tst", "test-output", "example.wav"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "speech.wav")
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newPythonService(ctx, c)
	defer s.Close()
	if err = s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	pid := s.command.Process.Pid
	for i := 0; i < 2; i++ {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		text, err := s.transcribe(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(text), "лукоморья") {
			t.Fatalf("unexpected transcript: %s", text)
		}
		if s.command.Process.Pid != pid {
			t.Fatal("model process changed")
		}
	}
	t.Logf("Two real transcriptions used one resident Python process: %d", pid)
}

func TestResidentRequestTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slow.py")
	code := "import json,sys,time\nprint(json.dumps({'type':'ready'}),flush=True)\nfor line in sys.stdin:\n time.sleep(30)\n"
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c := defaultTranscription()
	c.PythonExecutable = pythonForTest(t)
	c.LauncherPath = path
	c.TimeoutSeconds = 1
	s := newPythonService(context.Background(), c)
	defer s.Close()
	_, err := s.transcribe(context.Background(), filepath.Join(t.TempDir(), "input.wav"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout: %v", err)
	}
	if s.command != nil {
		t.Fatal("timed-out worker was not stopped")
	}
}
