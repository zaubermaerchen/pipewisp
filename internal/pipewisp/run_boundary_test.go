package pipewisp

// This file verifies the public Run boundary preserves binary stream data.

import (
	"bytes"
	"testing"
)

func TestRunPublicBoundaryPreservesBinaryInput(t *testing.T) {
	input := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, '\n'}
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := Run(nil, bytes.NewReader(input), &output, &diagnostics); got != 0 {
		t.Fatalf("Run() exit code = %d, want 0", got)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("Run() output = %x, want %x", output.Bytes(), input)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("Run() diagnostics = %q, want empty", diagnostics.String())
	}
}
