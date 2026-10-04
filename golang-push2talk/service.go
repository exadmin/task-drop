package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type serviceReply struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Text  string `json:"text"`
	Error string `json:"error"`
}
type lockedDiagnostics struct {
	mu   sync.Mutex
	tail tailWriter
}

func (d *lockedDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tail.Write(p)
}
func (d *lockedDiagnostics) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.tail.data)
}

type pythonService struct {
	mu          sync.Mutex
	lifetime    context.Context
	config      TranscriptionConfig
	command     *exec.Cmd
	stop        context.CancelFunc
	input       io.WriteCloser
	replies     chan serviceReply
	done        chan struct{}
	diagnostics *lockedDiagnostics
	sequence    uint64
}

func newPythonService(ctx context.Context, c TranscriptionConfig) *pythonService {
	return &pythonService{lifetime: ctx, config: c}
}

func (s *pythonService) reset() {
	if s.command == nil {
		return
	}
	s.stop()
	s.input.Close()
	<-s.done
	s.command = nil
}

func (s *pythonService) readReply(ctx context.Context) (serviceReply, error) {
	select {
	case reply, ok := <-s.replies:
		if ok {
			return reply, nil
		}
		return serviceReply{}, fmt.Errorf("Python service disconnected: %s", strings.TrimSpace(s.diagnostics.String()))
	case <-ctx.Done():
		return serviceReply{}, ctx.Err()
	}
}

// Caller holds mu across startup and each request; model calls remain sequential.
func (s *pythonService) ensureReady(ctx context.Context) error {
	if s.command != nil {
		select {
		case <-s.done:
			s.reset()
		default:
			return nil
		}
	}
	if err := s.lifetime.Err(); err != nil {
		return err
	}
	lifetime, stop := context.WithCancel(s.lifetime)
	command := exec.CommandContext(lifetime, s.config.PythonExecutable, "-X", "utf8", "-u", s.config.LauncherPath, "--server", "--model", s.config.Model, "--device", s.config.Device)
	configureChildProcess(command)
	command.Cancel = func() error { return stopChildProcess(command.Process) }
	command.WaitDelay = 5 * time.Second
	input, err := command.StdinPipe()
	if err != nil {
		stop()
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		stop()
		return err
	}
	diagnostics := &lockedDiagnostics{}
	command.Stderr = diagnostics
	if err = command.Start(); err != nil {
		input.Close()
		output.Close()
		stop()
		return err
	}
	replies := make(chan serviceReply, 1)
	done := make(chan struct{})
	s.command = command
	s.stop = stop
	s.input = input
	s.replies = replies
	s.done = done
	s.diagnostics = diagnostics
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(replies)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 16*1024*1024)
		for scanner.Scan() {
			var reply serviceReply
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				diagnostics.Write([]byte("Invalid service JSON: " + err.Error()))
				stop()
				return
			}
			select {
			case replies <- reply:
			case <-lifetime.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			diagnostics.Write([]byte(err.Error()))
		}
	}()
	go func() { <-readDone; _ = command.Wait(); close(done) }()
	reply, err := s.readReply(ctx)
	if err == nil && reply.Type != "ready" {
		err = fmt.Errorf("Python service did not send ready")
	}
	if err != nil {
		s.reset()
	}
	return err
}

func (s *pythonService) Ready(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.config.TimeoutSeconds)*time.Second)
	defer cancel()
	return s.ensureReady(ctx)
}

func (s *pythonService) transcribe(ctx context.Context, path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.config.TimeoutSeconds)*time.Second)
	defer cancel()
	if err := s.ensureReady(ctx); err != nil {
		return "", err
	}
	s.sequence++
	id := strconv.FormatUint(s.sequence, 10)
	request := struct {
		ID       string   `json:"id"`
		WavFiles []string `json:"wav_files"`
	}{id, []string{path}}
	if err := json.NewEncoder(s.input).Encode(request); err != nil {
		s.reset()
		return "", err
	}
	reply, err := s.readReply(ctx)
	if err != nil {
		s.reset()
		return "", err
	}
	if reply.ID != id {
		s.reset()
		return "", fmt.Errorf("Python service response ID mismatch")
	}
	if reply.Type == "error" {
		return "", fmt.Errorf("Python transcription: %s", reply.Error)
	}
	if reply.Type != "result" {
		s.reset()
		return "", fmt.Errorf("unexpected Python service message %q", reply.Type)
	}
	text := strings.TrimSpace(strings.ReplaceAll(reply.Text, "\x00", ""))
	if err = saveTranscript(path, s.config.StoreTextPath, text); err != nil {
		return "", err
	}
	return text, nil
}

func (s *pythonService) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.reset() }

// Reconnect proactively after a crash, with a delay to avoid a restart loop.
type serviceEvent struct {
	loading bool
	err     error
}

func (s *pythonService) maintain(ctx context.Context, status chan<- serviceEvent) {
	for ctx.Err() == nil {
		select {
		case status <- serviceEvent{loading: true}:
		case <-ctx.Done():
			return
		}
		err := s.Ready(ctx)
		select {
		case status <- serviceEvent{err: err}:
		case <-ctx.Done():
			return
		}
		if err == nil {
			s.mu.Lock()
			done := s.done
			s.mu.Unlock()
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}
