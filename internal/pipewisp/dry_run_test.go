package pipewisp

// This file verifies dry-run hook suppression and side-effect event records.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseArgsDryRun(t *testing.T) {
	got, help, err := parseArgs([]string{"--dry-run"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if !got.dryRun {
		t.Fatalf("parseArgs() dryRun = false, want true: %#v", got)
	}

	if _, _, err := parseArgs([]string{"--dry-run", "--dry-run"}); err == nil || !strings.Contains(err.Error(), "--dry-run specified more than once") {
		t.Fatalf("duplicate --dry-run error = %v, want duplicate option error", err)
	}
}

func TestDryRunPreservesInputAndSuppressesHooks(t *testing.T) {
	directory := t.TempDir()
	markers := []string{
		"ready.marker",
		"first-data.marker",
		"shutdown.marker",
	}
	commands := make([]string, len(markers))
	for i, marker := range markers {
		commands[i] = hookStartMarkerCommand(directory + string(os.PathSeparator) + marker)
	}

	input := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, '\n'}
	var output, diagnostics bytes.Buffer
	status := Run([]string{
		"--dry-run",
		"--on-ready", commands[0],
		"--on-first-data", commands[1],
		"--on-shutdown", commands[2],
	}, bytes.NewReader(input), &output, &diagnostics)
	if status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("stdout = %x, want %x", output.Bytes(), input)
	}
	for _, marker := range markers {
		if _, err := os.Stat(directory + string(os.PathSeparator) + marker); err == nil {
			t.Fatalf("dry-run hook created %s", marker)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", marker, err)
		}
	}
	want := "pipewisp: [DRY RUN] on-ready: " + strconv.Quote(commands[0]) + "\n" +
		"pipewisp: [DRY RUN] on-first-data: " + strconv.Quote(commands[1]) + "\n" +
		"pipewisp: [DRY RUN] on-shutdown: " + strconv.Quote(commands[2]) + "\n"
	if got := diagnostics.String(); got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestDryRunWithNameUsesNamedDiagnosticPrefix(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if status := Run([]string{"--dry-run", "--name", "relay", "--on-ready", "true"}, strings.NewReader(""), &output, &diagnostics); status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if got, want := diagnostics.String(), "pipewisp[relay]: [DRY RUN] on-ready: \"true\"\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestDryRunEscapesMultilineDiagnosticCommand(t *testing.T) {
	command := "printf 'line1\nline2\r\x1b'"
	var output, diagnostics bytes.Buffer
	if status := Run([]string{"--dry-run", "--on-ready", command}, strings.NewReader(""), &output, &diagnostics); status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if got, want := strings.Count(diagnostics.String(), "\n"), 1; got != want {
		t.Fatalf("diagnostic line count = %d, want %d: %q", got, want, diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), strconv.Quote(command)) {
		t.Fatalf("diagnostics = %q, want quoted command %q", diagnostics.String(), strconv.Quote(command))
	}
}

func TestRunEventsFDEmitsSideEffectRecords(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-dry-run-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	command := hookOutputCommand("ready")
	var output, diagnostics bytes.Buffer
	status := Run([]string{
		"--events-fd", stringFD(eventsFile),
		"--on-ready", command,
		"--on-shutdown", "true",
	}, strings.NewReader(""), &output, &diagnostics)
	if status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}

	records := decodeDryRunEventRecords(t, eventsFile)
	if got, want := len(records), 4; got != want {
		t.Fatalf("event count = %d, want %d: %#v", got, want, records)
	}
	wantEvents := []string{"ready", "side-effect", "shutdown", "side-effect"}
	for i, record := range records[:4] {
		if got := rawJSONText(t, record, "event"); got != wantEvents[i] {
			t.Errorf("event %d = %q, want %q", i, got, wantEvents[i])
		}
	}
	if got := rawJSONText(t, records[1], "type"); got != "execute-command" {
		t.Errorf("ready side effect type = %q, want execute-command", got)
	}
	if got := rawJSONText(t, records[1], "trigger"); got != "ready" {
		t.Errorf("ready side effect trigger = %q, want ready", got)
	}
	if got := rawJSONText(t, records[1], "command"); got != command {
		t.Errorf("ready side effect command = %q, want %q", got, command)
	}
	if got := rawJSONBool(t, records[1], "executed"); !got {
		t.Error("ready side effect executed = false, want true")
	}
	if _, ok := records[1]["reason"]; ok {
		t.Errorf("successful side effect has reason: %#v", records[1])
	}
}

func TestRunEventsFDDryRunSideEffectRecords(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-dry-run-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	command := `printf 'not-run'`
	var output, diagnostics bytes.Buffer
	status := Run([]string{
		"--dry-run",
		"--events-fd", stringFD(eventsFile),
		"--on-ready", command,
		"--on-first-data", "true",
		"--on-shutdown", "true",
	}, strings.NewReader("input"), &output, &diagnostics)
	if status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	wantDiagnostics := "pipewisp: [DRY RUN] on-ready: " + strconv.Quote(command) + "\n" +
		"pipewisp: [DRY RUN] on-first-data: \"true\"\n" +
		"pipewisp: [DRY RUN] on-shutdown: \"true\"\n"
	if got := diagnostics.String(); got != wantDiagnostics {
		t.Fatalf("diagnostics = %q, want %q", got, wantDiagnostics)
	}

	records := decodeDryRunEventRecords(t, eventsFile)
	if got, want := len(records), 6; got != want {
		t.Fatalf("event count = %d, want %d: %#v", got, want, records)
	}
	wantEvents := []string{"ready", "side-effect", "first-data", "side-effect", "shutdown", "side-effect"}
	for i, want := range wantEvents {
		if got := rawJSONText(t, records[i], "event"); got != want {
			t.Errorf("event %d = %q, want %q", i, got, want)
		}
		if want != "side-effect" {
			continue
		}
		if got := rawJSONBool(t, records[i], "executed"); got {
			t.Errorf("dry-run side effect %d executed = true, want false", i)
		}
		if got := rawJSONText(t, records[i], "reason"); got != "dry-run" {
			t.Errorf("dry-run side effect %d reason = %q, want dry-run", i, got)
		}
	}
}

func TestAsyncHookEmitsNormalSideEffectRecord(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-async-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	var diagnostics bytes.Buffer
	emitter := newEventEmitter(int(eventsFile.Fd()), &diagnostics)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	manager := newAsyncHookManager(io.Discard)
	manager.events = emitter
	manager.start("on-idle", "true", hookContext{event: "idle"}, 0)
	manager.stopAndWait()
	emitter.close()

	records := decodeDryRunEventRecords(t, eventsFile)
	if got, want := len(records), 1; got != want {
		t.Fatalf("event count = %d, want %d: %#v", got, want, records)
	}
	if got := rawJSONText(t, records[0], "event"); got != "side-effect" {
		t.Fatalf("event = %q, want side-effect", got)
	}
	if got := rawJSONText(t, records[0], "trigger"); got != "idle" {
		t.Fatalf("trigger = %q, want idle", got)
	}
	if got := rawJSONText(t, records[0], "command"); got != "true" {
		t.Fatalf("command = %q, want true", got)
	}
	if !rawJSONBool(t, records[0], "executed") {
		t.Fatal("executed = false, want true")
	}
	if _, ok := records[0]["reason"]; ok {
		t.Fatalf("successful side-effect has reason: %#v", records[0])
	}
}

func TestSideEffectRemainsExecutedForHookOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		args       []string
		wantStatus int
	}{
		{name: "nonzero", command: failingHookCommand("nonzero"), wantStatus: 1},
		{name: "timeout", command: hookSleepCommand(time.Second), args: []string{"--hook-timeout", "20ms"}, wantStatus: 1},
		{name: "ignored nonzero", command: failingHookCommand("ignored"), args: []string{"--ignore-hook-errors"}, wantStatus: 0},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name       string
			command    string
			args       []string
			wantStatus int
		}{name: "signal", command: "kill -TERM $$", wantStatus: 1})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eventsFile, err := os.CreateTemp("", "pipewisp-outcome-events-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = eventsFile.Close()
				_ = os.Remove(eventsFile.Name())
			}()

			args := []string{"--events-fd", stringFD(eventsFile), "--on-ready", test.command}
			args = append(args, test.args...)
			var output, diagnostics bytes.Buffer
			if status := Run(args, strings.NewReader(""), &output, &diagnostics); status != test.wantStatus {
				t.Fatalf("Run() status = %d, want %d; diagnostics = %q", status, test.wantStatus, diagnostics.String())
			}
			records := decodeDryRunEventRecords(t, eventsFile)
			var sideEffect map[string]json.RawMessage
			for _, record := range records {
				if rawJSONText(t, record, "event") == "side-effect" {
					sideEffect = record
					break
				}
			}
			if sideEffect == nil {
				t.Fatalf("records = %#v, missing side-effect record", records)
			}
			if !rawJSONBool(t, sideEffect, "executed") {
				t.Fatalf("side-effect = %#v, executed = false, want true", sideEffect)
			}
			if _, ok := sideEffect["reason"]; ok {
				t.Fatalf("start-successful side-effect has reason: %#v", sideEffect)
			}
		})
	}
}

func TestVerboseHookStartIsWrittenBeforeSyncHookStarts(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-sync-verbose-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	var output, diagnostics bytes.Buffer
	status := Run([]string{
		"--verbose",
		"--events-fd", stringFD(eventsFile),
		"--on-ready", hookOutputCommand("sync-hook-output"),
	}, strings.NewReader(""), &output, &diagnostics)
	if status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	assertHookStartPrecedesOutput(t, diagnostics.String(), "ready", "sync-hook-output")
}

func TestVerboseHookStartIsWrittenBeforeAsyncHookStarts(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-async-verbose-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	var diagnostics bytes.Buffer
	diagnosticEvents := make(chan string, 16)
	diagnosticOutput := io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: diagnosticEvents})
	emitter := newEventEmitter(int(eventsFile.Fd()), diagnosticOutput)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	manager := newAsyncHookManager(newVerboseReporter(diagnosticOutput, true))
	manager.events = emitter
	manager.start("on-idle", hookOutputCommand("async-hook-output"), hookContext{event: "idle"}, 0)
	waitForEvent(t, diagnosticEvents, "async-hook-output")
	manager.stopAndWait()
	emitter.close()

	assertHookStartPrecedesOutput(t, diagnostics.String(), "idle", "async-hook-output")
}

func assertHookStartPrecedesOutput(t *testing.T, diagnostics, event, output string) {
	t.Helper()
	start := strings.Index(diagnostics, "type=hook event="+event+" state=start")
	if start < 0 {
		t.Fatalf("diagnostics = %q, missing hook start", diagnostics)
	}
	outputAt := strings.Index(diagnostics, output)
	if outputAt < 0 {
		t.Fatalf("diagnostics = %q, missing hook output", diagnostics)
	}
	if start > outputAt {
		t.Fatalf("diagnostics = %q, hook start follows hook output", diagnostics)
	}
}

func TestHookTimeoutStopsProcessWhileSideEffectCallbackBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the short POSIX sleep makes the process-stop observation deterministic")
	}
	const timeout = 50 * time.Millisecond
	const processDuration = time.Second

	directory := t.TempDir()
	startedPath := directory + string(os.PathSeparator) + "started"
	completedPath := directory + string(os.PathSeparator) + "completed"
	command := hookStartMarkerCommand(startedPath) + hookSleepCommand(processDuration) + hookMarkerCommand(completedPath)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	callbackEntered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControlAndSideEffect(
			command,
			hookContext{event: "ready"},
			io.Discard,
			timeout,
			nil,
			nil,
			func(executed bool, reason string) {
				if !executed || reason != "" {
					return
				}
				close(callbackEntered)
				<-release
			},
		)
	}()

	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("side-effect callback was not reached after process start")
	}
	waitForHookFile(t, startedPath)
	time.Sleep(processDuration + 100*time.Millisecond)
	if _, err := os.Stat(completedPath); err == nil {
		t.Fatal("hook process completed while side-effect callback was blocked")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat completed marker: %v", err)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("hook result = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("hook did not finish after side-effect callback was released")
	}
}

func TestHookDirectlyForwardsLargeOutputWithEventsFD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dd is not available on Windows")
	}
	const chunkSize = 128 * 1024
	command := "dd if=/dev/zero bs=" + strconv.Itoa(chunkSize) + " count=1 2>/dev/null; dd if=/dev/zero bs=" + strconv.Itoa(chunkSize) + " count=1 1>&2 2>/dev/null"
	eventsFile, err := os.CreateTemp("", "pipewisp-large-hook-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	var output, diagnostics bytes.Buffer
	if status := Run([]string{"--events-fd", stringFD(eventsFile), "--on-ready", command}, strings.NewReader(""), &output, &diagnostics); status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics length = %d", status, diagnostics.Len())
	}
	if got, want := diagnostics.Len(), 2*chunkSize; got != want {
		t.Fatalf("diagnostics length = %d, want %d", got, want)
	}
}

func TestDryRunSuppressesAsyncHooksAndPreservesIdleLifecycle(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()

	records := make(chan map[string]json.RawMessage, 16)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		decoder := json.NewDecoder(readEvents)
		for {
			var record map[string]json.RawMessage
			if err := decoder.Decode(&record); err != nil {
				return
			}
			records <- record
		}
	}()

	marker := t.TempDir() + string(os.PathSeparator) + "async.marker"
	command := hookStartMarkerCommand(marker)
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output, diagnostics bytes.Buffer
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{
			"--dry-run",
			"--events-fd", stringFD(writeEvents),
			"--idle", "5ms",
			"--on-idle.async", command,
		}, input, &output, &diagnostics)
	}()

	for _, want := range []string{"ready", "first-data", "idle", "side-effect"} {
		select {
		case record := <-records:
			if got := rawJSONText(t, record, "event"); got != want {
				t.Fatalf("event = %q, want %q", got, want)
			}
			if want == "side-effect" {
				if got := rawJSONText(t, record, "trigger"); got != "idle" {
					t.Errorf("async side effect trigger = %q, want idle", got)
				}
				if got := rawJSONText(t, record, "reason"); got != "dry-run" {
					t.Errorf("async dry-run reason = %q, want dry-run", got)
				}
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s event", want)
		}
	}

	close(input.secondReady)
	for _, want := range []string{"resume", "shutdown"} {
		select {
		case record := <-records:
			if got := rawJSONText(t, record, "event"); got != want {
				t.Fatalf("event = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s event", want)
		}
	}
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
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry-run async hook created marker")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat async marker: %v", err)
	}
	if !strings.Contains(diagnostics.String(), "[DRY RUN] on-idle: "+strconv.Quote(command)) {
		t.Fatalf("diagnostics = %q, want dry-run async report", diagnostics.String())
	}
	_ = writeEvents.Close()
	<-readDone
}

func TestDryRunSuppressesSyncIdleAndResumeHooks(t *testing.T) {
	directory := t.TempDir()
	idleMarker := directory + string(os.PathSeparator) + "idle.marker"
	resumeMarker := directory + string(os.PathSeparator) + "resume.marker"
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	diagnosticEvents := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: diagnosticEvents}
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{
			"--dry-run",
			"--idle", "5ms",
			"--on-idle", hookStartMarkerCommand(idleMarker),
			"--on-resume", hookStartMarkerCommand(resumeMarker),
			"--on-shutdown", "true",
		}, input, &output, diagnostics)
	}()
	idleReportSeen := false
	var diagnosticText strings.Builder
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !idleReportSeen {
		select {
		case diagnostic := <-diagnosticEvents:
			diagnosticText.WriteString(diagnostic)
			idleReportSeen = strings.Contains(diagnostic, "[DRY RUN] on-idle")
		case <-deadline.C:
			t.Fatal("sync idle dry-run report was not emitted")
		}
	}
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish after release")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, marker := range []string{idleMarker, resumeMarker} {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("dry-run hook created %s", marker)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", marker, err)
		}
	}
	for {
		select {
		case diagnostic := <-diagnosticEvents:
			diagnosticText.WriteString(diagnostic)
		default:
			if !strings.Contains(diagnosticText.String(), "[DRY RUN] on-idle: "+strconv.Quote(hookStartMarkerCommand(idleMarker))) {
				t.Fatalf("diagnostics = %q, missing idle dry-run report", diagnosticText.String())
			}
			if !strings.Contains(diagnosticText.String(), "[DRY RUN] on-resume: "+strconv.Quote(hookStartMarkerCommand(resumeMarker))) {
				t.Fatalf("diagnostics = %q, missing resume dry-run report", diagnosticText.String())
			}
			return
		}
	}
}

func TestDryRunContinuesAfterEventWriteFailure(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = readEvents.Close()
	defer writeEvents.Close()

	var output, diagnostics bytes.Buffer
	status := Run([]string{
		"--dry-run",
		"--events-fd", stringFD(writeEvents),
		"--on-ready", "true",
		"--on-shutdown", "true",
	}, strings.NewReader("input"), &output, &diagnostics)
	if status != 0 {
		t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
	if got := strings.Count(diagnostics.String(), "[DRY RUN]"); got != 2 {
		t.Fatalf("diagnostics = %q, want both dry-run reports", diagnostics.String())
	}
}

func stringFD(file *os.File) string {
	return strconv.FormatUint(uint64(file.Fd()), 10)
}

func decodeDryRunEventRecords(t *testing.T, file *os.File) []map[string]json.RawMessage {
	t.Helper()
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(file)
	var records []map[string]json.RawMessage
	for {
		var record map[string]json.RawMessage
		err := decoder.Decode(&record)
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatalf("decode event record: %v", err)
		}
		records = append(records, record)
	}
}

func rawJSONText(t *testing.T, record map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return value
}

func rawJSONBool(t *testing.T, record map[string]json.RawMessage, key string) bool {
	t.Helper()
	var value bool
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return value
}
