package main

// This file parses command-line options and renders the usage text.

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type options struct {
	on             string
	onSet          bool
	onFirstData    string
	onFirstDataSet bool
	off            string
	offSet         bool
	idle           time.Duration
	idleSet        bool
	onIdle         string
	onIdleSet      bool
	onResume       string
	onResumeSet    bool
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
		case arg == "--idle":
			if opts.idleSet {
				return options{}, false, fmt.Errorf("--idle specified more than once")
			}
			value, next, err := parseSeparateDuration(args, i, "--idle")
			if err != nil {
				return options{}, false, err
			}
			opts.idle, opts.idleSet = value, true
			i = next
		case strings.HasPrefix(arg, "--idle="):
			if opts.idleSet {
				return options{}, false, fmt.Errorf("--idle specified more than once")
			}
			value, err := parseDuration(arg[len("--idle="):], "--idle")
			if err != nil {
				return options{}, false, err
			}
			opts.idle, opts.idleSet = value, true
		case arg == "--on-idle":
			if opts.onIdleSet {
				return options{}, false, fmt.Errorf("--on-idle specified more than once")
			}
			value, next, err := parseSeparateValue(args, i, "--on-idle")
			if err != nil {
				return options{}, false, err
			}
			opts.onIdle, opts.onIdleSet = value, true
			i = next
		case strings.HasPrefix(arg, "--on-idle="):
			if opts.onIdleSet {
				return options{}, false, fmt.Errorf("--on-idle specified more than once")
			}
			value, err := validateCommand(arg[len("--on-idle="):], "--on-idle")
			if err != nil {
				return options{}, false, err
			}
			opts.onIdle, opts.onIdleSet = value, true
		case arg == "--on-resume":
			if opts.onResumeSet {
				return options{}, false, fmt.Errorf("--on-resume specified more than once")
			}
			value, next, err := parseSeparateValue(args, i, "--on-resume")
			if err != nil {
				return options{}, false, err
			}
			opts.onResume, opts.onResumeSet = value, true
			i = next
		case strings.HasPrefix(arg, "--on-resume="):
			if opts.onResumeSet {
				return options{}, false, fmt.Errorf("--on-resume specified more than once")
			}
			value, err := validateCommand(arg[len("--on-resume="):], "--on-resume")
			if err != nil {
				return options{}, false, err
			}
			opts.onResume, opts.onResumeSet = value, true
		case strings.HasPrefix(arg, "-"):
			return options{}, false, fmt.Errorf("unknown option %s", arg)
		default:
			return options{}, false, fmt.Errorf("unexpected positional argument %s", arg)
		}
	}

	if opts.idleSet && opts.idle <= 0 {
		return options{}, false, fmt.Errorf("--idle must be greater than zero")
	}
	if opts.onIdleSet && !opts.idleSet {
		return options{}, false, fmt.Errorf("--on-idle requires --idle")
	}
	if opts.onResumeSet && !opts.idleSet {
		return options{}, false, fmt.Errorf("--on-resume requires --idle")
	}
	if opts.idleSet && !opts.onIdleSet && !opts.onResumeSet {
		return options{}, false, fmt.Errorf("--idle requires --on-idle or --on-resume")
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

func parseSeparateDuration(args []string, optionIndex int, option string) (time.Duration, int, error) {
	valueIndex := optionIndex + 1
	if valueIndex >= len(args) {
		return 0, optionIndex, fmt.Errorf("missing value for %s", option)
	}
	if strings.HasPrefix(args[valueIndex], "--") {
		return 0, optionIndex, fmt.Errorf("missing value for %s", option)
	}
	value, err := parseDuration(args[valueIndex], option)
	if err != nil {
		return 0, optionIndex, err
	}
	return value, valueIndex, nil
}

func parseDuration(value, option string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("empty duration for %s", option)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %w", option, err)
	}
	return duration, nil
}

func validateCommand(command, option string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty command for %s", option)
	}
	return command, nil
}

func printUsage(out io.Writer) {
	_, _ = out.Write([]byte("Usage: pipewisp [--on COMMAND] [--on-first-data COMMAND] [--off COMMAND] [--idle DURATION] [--on-idle COMMAND] [--on-resume COMMAND]\n"))
}
