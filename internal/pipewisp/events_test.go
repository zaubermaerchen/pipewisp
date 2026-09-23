//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || windows

package pipewisp

// This file tests the machine-readable lifecycle event stream.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunEventsFDEmitsLifecycleEventsWithoutHooks(t *testing.T) {
	eventsFile, eventsWrite, finishEvents := newEventCapture(t)

	var output, diagnostics bytes.Buffer
	fdArg := fmt.Sprintf("--events-fd=%d", testEventFD(t, eventsWrite))
	if status := Run([]string{fdArg}, strings.NewReader("input"), &output, &diagnostics); status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q, want empty", diagnostics.String())
	}

	finishEvents()
	var records []map[string]string
	decoder := json.NewDecoder(eventsFile)
	for {
		var record map[string]string
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if got, want := len(records), 3; got != want {
		t.Fatalf("event count = %d, want %d: %#v", got, want, records)
	}
	for i, wantEvent := range []string{"ready", "first-data", "shutdown"} {
		if got := records[i]["event"]; got != wantEvent {
			t.Errorf("event %d = %q, want %q", i, got, wantEvent)
		}
		if len(records[i]) != 2 {
			t.Errorf("event %d fields = %#v, want only event and timestamp", i, records[i])
		}
		if _, err := time.Parse(time.RFC3339Nano, records[i]["timestamp"]); err != nil {
			t.Errorf("event %d timestamp = %q: %v", i, records[i]["timestamp"], err)
		}
	}
}

func TestRunEventsFDInvalidDescriptorContinuesPipeline(t *testing.T) {
	input := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, '\n'}
	source := &writerToReader{data: append([]byte(nil), input...)}
	var output, diagnostics bytes.Buffer

	invalidFD := strconv.Itoa(int(^uint(0) >> 1))
	if status := Run([]string{"--events-fd", invalidFD}, source, &output, &diagnostics); status != 2 {
		t.Fatalf("Run() status = %d, want 2; diagnostics = %q", status, diagnostics.String())
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %x, want empty", output.Bytes())
	}
	if got := strings.Count(diagnostics.String(), "invalid --events-fd"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
	if source.writeToCalled {
		t.Fatal("invalid events fd read stdin")
	}
}

func TestRunEventsFDEnablesIdleLifecycleEventsWithoutHooks(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	defer readEvents.Close()

	records := make(chan lifecycleEventRecord, 16)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		decoder := json.NewDecoder(readEvents)
		for {
			var record lifecycleEventRecord
			if err := decoder.Decode(&record); err != nil {
				return
			}
			records <- record
		}
	}()

	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output, diagnostics bytes.Buffer
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{
			"--events-fd", strconv.FormatUint(uint64(testEventFD(t, writeEvents)), 10),
			"--idle", "5ms",
		}, input, &output, &diagnostics)
	}()

	for _, want := range []string{"ready", "first-data", "idle"} {
		select {
		case record := <-records:
			if record.Event != want {
				t.Fatalf("event = %q, want %q", record.Event, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s event", want)
		}
	}
	close(input.secondReady)

	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	_ = writeEvents.Close()
	<-readDone

	var remaining []string
	for {
		select {
		case record := <-records:
			remaining = append(remaining, record.Event)
		default:
			want := []string{"resume", "shutdown"}
			if len(remaining) != len(want) || remaining[0] != want[0] || remaining[1] != want[1] {
				t.Fatalf("remaining events = %v, want %v", remaining, want)
			}
			return
		}
	}
}

func TestEventEmitterDisablesAfterWriteFailure(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	_ = readEvents.Close()
	defer writeEvents.Close()

	var diagnostics bytes.Buffer
	emitter := newEventEmitter(int(testEventFD(t, writeEvents)), &diagnostics)
	emitter.emit("ready")
	emitter.emit("shutdown")
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
}

func TestEventEmitterDoesNotCloseBorrowedFD(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	var diagnostics bytes.Buffer
	emitter := newEventEmitter(int(eventsFile.Fd()), &diagnostics)
	emitter.emit("ready")
	runtime.GC()
	if _, err := eventsFile.Stat(); err != nil {
		t.Fatalf("borrowed fd was closed: %v", err)
	}
}

func TestEventsWarningSharesDiagnosticSynchronizerWithAsyncHooks(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	defer readEvents.Close()
	defer writeEvents.Close()

	eventRecords := make(chan lifecycleEventRecord, 8)
	go func() {
		decoder := json.NewDecoder(readEvents)
		for {
			var record lifecycleEventRecord
			if err := decoder.Decode(&record); err != nil {
				return
			}
			eventRecords <- record
		}
	}()

	diagnostics := &diagnosticOverlapWriter{
		entered: make(chan string, 4),
		release: make(chan struct{}),
	}
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{
			"--events-fd", strconv.FormatUint(uint64(testEventFD(t, writeEvents)), 10),
			"--idle", "5ms",
			"--on-idle.async", delayedFailingHookCommand(200 * time.Millisecond),
		}, input, io.Discard, diagnostics)
	}()

	for _, want := range []string{"ready", "first-data", "idle"} {
		select {
		case record := <-eventRecords:
			if record.Event != want {
				t.Fatalf("event = %q, want %q", record.Event, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s event", want)
		}
	}
	_ = readEvents.Close()
	close(input.secondReady)

	select {
	case kind := <-diagnostics.entered:
		if kind != "warning" {
			t.Fatalf("first blocked diagnostic = %q, want warning", kind)
		}
	case <-time.After(time.Second):
		t.Fatal("event failure warning did not reach diagnostics")
	}

	overlapped := false
	select {
	case kind := <-diagnostics.entered:
		overlapped = kind == "async"
	case <-time.After(300 * time.Millisecond):
	}
	close(diagnostics.release)

	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if overlapped {
		t.Fatal("async hook diagnostics overlapped event failure warning")
	}
}

func TestEventsWarningSharesDiagnosticSynchronizerWithSyncHookOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the large POSIX hook output makes the overlap deterministic")
	}
	readEvents, writeEvents := newObservationPipe(t)
	defer writeEvents.Close()

	diagnostics := &diagnosticConcurrencyWriter{
		warningEntered: make(chan struct{}),
		hookOutputSeen: make(chan struct{}),
		release:        make(chan struct{}),
	}
	gatePath := t.TempDir() + string(os.PathSeparator) + "release"
	command := "while [ ! -e " + unixQuote(gatePath) + " ]; do :; done; " + hookOutputCommand(strings.Repeat("hook-output", 8192))
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{
			"--events-fd", strconv.FormatUint(uint64(testEventFD(t, writeEvents)), 10),
			"--on-ready", command,
		}, strings.NewReader(""), io.Discard, diagnostics)
	}()

	var ready lifecycleEventRecord
	if err := json.NewDecoder(readEvents).Decode(&ready); err != nil {
		t.Fatalf("decode ready event: %v", err)
	}
	if ready.Event != "ready" {
		t.Fatalf("first event = %q, want ready", ready.Event)
	}
	_ = readEvents.Close()

	select {
	case <-diagnostics.warningEntered:
	case <-time.After(time.Second):
		diagnostics.releaseOnce.Do(func() { close(diagnostics.release) })
		t.Fatal("event failure warning did not reach diagnostics")
	}
	if err := os.WriteFile(gatePath, nil, 0600); err != nil {
		diagnostics.releaseOnce.Do(func() { close(diagnostics.release) })
		t.Fatalf("write hook output gate: %v", err)
	}
	select {
	case <-diagnostics.hookOutputSeen:
		diagnostics.releaseOnce.Do(func() { close(diagnostics.release) })
		t.Fatal("sync hook output overlapped the event failure warning")
	case <-time.After(200 * time.Millisecond):
	}
	diagnostics.releaseOnce.Do(func() { close(diagnostics.release) })

	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish after releasing diagnostics")
	}
	if diagnostics.overlap.Load() {
		t.Fatal("diagnostic writes overlapped")
	}
}

func delayedFailingHookCommand(duration time.Duration) string {
	if os.PathSeparator == '\\' {
		return hookSleepCommand(duration) + " & exit /b 7"
	}
	return hookSleepCommand(duration) + "; exit 7"
}

type diagnosticOverlapWriter struct {
	entered chan string
	release chan struct{}
	active  atomic.Int32
	output  bytes.Buffer
}

type diagnosticConcurrencyWriter struct {
	warningEntered chan struct{}
	hookOutputSeen chan struct{}
	release        chan struct{}
	releaseOnce    sync.Once
	warningOnce    sync.Once
	hookOutputOnce sync.Once
	warningActive  atomic.Bool
	active         atomic.Int32
	overlap        atomic.Bool
}

func (writer *diagnosticConcurrencyWriter) Write(p []byte) (int, error) {
	if writer.active.Add(1) > 1 {
		writer.overlap.Store(true)
	}
	defer writer.active.Add(-1)
	if strings.Contains(string(p), "events disabled") {
		writer.warningActive.Store(true)
		writer.warningOnce.Do(func() { close(writer.warningEntered) })
		<-writer.release
		writer.warningActive.Store(false)
	} else {
		if writer.warningActive.Load() {
			writer.hookOutputOnce.Do(func() { close(writer.hookOutputSeen) })
		}
	}
	return len(p), nil
}

func (writer *diagnosticOverlapWriter) Write(p []byte) (int, error) {
	text := string(p)
	kind := ""
	if strings.Contains(text, "events disabled") {
		kind = "warning"
	} else if strings.Contains(text, "on-idle hook failed") {
		kind = "async"
	}
	if kind != "" {
		writer.active.Add(1)
		writer.entered <- kind
		<-writer.release
	}
	n, err := writer.output.Write(p)
	if kind != "" {
		writer.active.Add(-1)
	}
	return n, err
}

func rawEventFD(t *testing.T, file *os.File) uintptr {
	t.Helper()
	connection, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fd uintptr
	if err := connection.Control(func(raw uintptr) { fd = raw }); err != nil {
		t.Fatal(err)
	}
	return fd
}
