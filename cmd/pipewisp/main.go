package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(in io.Reader, out io.Writer, diagnostics io.Writer) int {
	if _, err := io.Copy(out, in); err != nil {
		_, _ = fmt.Fprintf(diagnostics, "pipewisp: %v\n", err)
		return 1
	}
	return 0
}
