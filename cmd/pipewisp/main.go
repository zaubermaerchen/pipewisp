// Package main is the thin pipewisp executable adapter.
package main

import (
	"os"

	"github.com/zaubermaerchen/pipewisp/internal/pipewisp"
)

func main() {
	os.Exit(pipewisp.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
