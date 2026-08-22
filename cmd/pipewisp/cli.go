package main

// This file parses command-line options and renders the usage text.

import (
	"fmt"
	"io"
	"strings"
)

type options struct {
	on             string
	onSet          bool
	onFirstData    string
	onFirstDataSet bool
	off            string
	offSet         bool
}

func parseArgs(args []string) (options, bool, error) {
	var opts options

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			if len(args) != 1 {
				return options{}, false, fmt.Errorf("%s cannot be combined with other arguments", arg)
			}
			return options{}, true, nil
		case arg == "--on":
			if opts.onSet {
				return options{}, false, fmt.Errorf("--on specified more than once")
			}
			value, next, err := parseSeparateValue(args, i, "--on")
			if err != nil {
				return options{}, false, err
			}
			opts.on, opts.onSet = value, true
			i = next
		case strings.HasPrefix(arg, "--on="):
			if opts.onSet {
				return options{}, false, fmt.Errorf("--on specified more than once")
			}
			value, err := validateCommand(arg[len("--on="):], "--on")
			if err != nil {
				return options{}, false, err
			}
			opts.on, opts.onSet = value, true
		case arg == "--on-first-data":
			if opts.onFirstDataSet {
				return options{}, false, fmt.Errorf("--on-first-data specified more than once")
			}
			value, next, err := parseSeparateValue(args, i, "--on-first-data")
			if err != nil {
				return options{}, false, err
			}
			opts.onFirstData, opts.onFirstDataSet = value, true
			i = next
		case strings.HasPrefix(arg, "--on-first-data="):
			if opts.onFirstDataSet {
				return options{}, false, fmt.Errorf("--on-first-data specified more than once")
			}
			value, err := validateCommand(arg[len("--on-first-data="):], "--on-first-data")
			if err != nil {
				return options{}, false, err
			}
			opts.onFirstData, opts.onFirstDataSet = value, true
		case arg == "--off":
			if opts.offSet {
				return options{}, false, fmt.Errorf("--off specified more than once")
			}
			value, next, err := parseSeparateValue(args, i, "--off")
			if err != nil {
				return options{}, false, err
			}
			opts.off, opts.offSet = value, true
			i = next
		case strings.HasPrefix(arg, "--off="):
			if opts.offSet {
				return options{}, false, fmt.Errorf("--off specified more than once")
			}
			value, err := validateCommand(arg[len("--off="):], "--off")
			if err != nil {
				return options{}, false, err
			}
			opts.off, opts.offSet = value, true
		case strings.HasPrefix(arg, "-"):
			return options{}, false, fmt.Errorf("unknown option %s", arg)
		default:
			return options{}, false, fmt.Errorf("unexpected positional argument %s", arg)
		}
	}

	return opts, false, nil
}

func parseSeparateValue(args []string, optionIndex int, option string) (string, int, error) {
	valueIndex := optionIndex + 1
	if valueIndex >= len(args) || strings.HasPrefix(args[valueIndex], "-") {
		return "", optionIndex, fmt.Errorf("missing value for %s", option)
	}
	value, err := validateCommand(args[valueIndex], option)
	if err != nil {
		return "", optionIndex, err
	}
	return value, valueIndex, nil
}

func validateCommand(command, option string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty command for %s", option)
	}
	return command, nil
}

func printUsage(out io.Writer) {
	_, _ = out.Write([]byte("Usage: pipewisp [--on COMMAND] [--on-first-data COMMAND] [--off COMMAND]\n"))
}
