package main

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestSelectCompletionPrefersReadySignal(t *testing.T) {
	copyDone := make(chan error, 1)
	signals := make(chan os.Signal, 1)
	copyDone <- nil
	signals <- os.Interrupt

	got := selectCompletion(copyDone, signals)
	if got.signal != os.Interrupt {
		t.Fatalf("selectCompletion() signal = %v, want %v", got.signal, os.Interrupt)
	}
	if got.copyErr != nil {
		t.Fatalf("selectCompletion() copy error = %v, want nil", got.copyErr)
	}
}

func TestSelectCompletionReturnsCopyResult(t *testing.T) {
	wantErr := errors.New("copy failed")
	copyDone := make(chan error, 1)
	signals := make(chan os.Signal, 1)
	copyDone <- wantErr

	got := selectCompletion(copyDone, signals)
	if !errors.Is(got.copyErr, wantErr) {
		t.Fatalf("selectCompletion() copy error = %v, want %v", got.copyErr, wantErr)
	}
	if got.signal != nil {
		t.Fatalf("selectCompletion() signal = %v, want nil", got.signal)
	}
}

func TestClassifyCompletion(t *testing.T) {
	tests := []struct {
		name       string
		completion completion
		want       completionKind
	}{
		{name: "success", want: completionSuccess},
		{name: "copy error", completion: completion{copyErr: errors.New("copy failed")}, want: completionCopyError},
		{name: "signal", completion: completion{signal: os.Interrupt}, want: completionSignal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyCompletion(tt.completion); got != tt.want {
				t.Fatalf("classifyCompletion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFinishCompletionRunsOffOnce(t *testing.T) {
	completions := []completion{
		{},
		{copyErr: errors.New("copy failed")},
		{signal: os.Interrupt},
	}

	for _, completion := range completions {
		var diagnostics bytes.Buffer
		calls := 0
		status := finishCompletion(completion, func() error {
			calls++
			return nil
		}, &diagnostics)
		if calls != 1 {
			t.Errorf("finishCompletion() off calls = %d, want 1", calls)
		}
		if completion.signal != nil && status != 130 {
			t.Errorf("finishCompletion() signal status = %d, want 130", status)
		}
	}
}

func TestFinishCompletionSignalStatusWinsOffFailure(t *testing.T) {
	var diagnostics bytes.Buffer
	status := finishCompletion(completion{signal: os.Interrupt}, func() error {
		err := errors.New("off failed")
		reportDiagnostic(&diagnostics, err)
		return err
	}, &diagnostics)

	if status != 130 {
		t.Fatalf("finishCompletion() status = %d, want 130", status)
	}
	if diagnostics.Len() == 0 {
		t.Fatal("finishCompletion() diagnostics are empty, want off failure")
	}
}

func TestFinishCompletionRecordsSignalDuringOff(t *testing.T) {
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	status := finishCompletionWithTracker(completion{}, func() error {
		signals <- os.Interrupt
		return errors.New("off failed")
	}, &diagnostics, tracker)

	if status != 130 {
		t.Fatalf("finishCompletionWithTracker() status = %d, want 130", status)
	}
}

func TestSignalTrackerKeepsFirstSignal(t *testing.T) {
	signals := make(chan os.Signal, 2)
	first := testSignal("first")
	second := testSignal("second")
	signals <- first
	signals <- second
	tracker := &signalTracker{signals: signals}

	if got := tracker.poll(); got != first {
		t.Fatalf("signalTracker.poll() = %v, want %v", got, first)
	}
	if got := tracker.first; got != first {
		t.Fatalf("signalTracker.first = %v, want %v", got, first)
	}
}

type testSignal string

func (s testSignal) Signal() {}

func (s testSignal) String() string { return string(s) }
