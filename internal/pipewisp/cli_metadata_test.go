package pipewisp

// This file keeps descriptive option metadata aligned with the explicit parser.

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCLIOptionForms(t *testing.T) {
	var usage bytes.Buffer
	printUsage(&usage)
	for _, option := range newDescription().CLISchema.Options {
		t.Run(option.Name, func(t *testing.T) {
			names := append([]string{option.Name}, option.Aliases...)
			for _, name := range names {
				metavar := map[string]string{"string": "NAME", "fd": "FD", "command": "COMMAND", "duration": "DURATION"}[option.Type]
				helpForm := "[" + name
				if metavar != "" {
					helpForm += " " + metavar
				}
				helpForm += "]"
				if name == "--describe" || name == "--version" {
					helpForm = "       pipewisp " + name + "\n"
				}
				if name != "--help" && name != "-h" && !strings.Contains(usage.String(), helpForm) {
					t.Errorf("help does not describe %s as %q", name, helpForm)
				}
				value := map[string]string{"string": "worker", "fd": "3", "command": "echo ready", "duration": "1s"}[option.Type]
				forms := [][]string{{name}}
				if value != "" {
					forms = [][]string{{name, value}, {name + "=" + value}}
				}
				for _, form := range forms {
					args := withCLIPrerequisites(option.Name, form)
					if _, _, err := parseArgs(args); err != nil {
						t.Errorf("parseArgs(%q): %v", args, err)
					}
				}
				if option.Type == "boolean" {
					args := []string{name + "=true"}
					_, _, err := parseArgs(args)
					want := "unknown option " + args[0]
					if err == nil || err.Error() != want {
						t.Errorf("parseArgs(%q) error = %v, want %q", args, err, want)
					}
				}
			}
		})
	}
}

func withCLIPrerequisites(name string, args []string) []string {
	switch name {
	case "--idle":
		return append(args, "--verbose")
	case "--on-idle", "--on-idle.async", "--on-resume", "--on-resume.async":
		return append(args, "--idle=1s")
	default:
		return args
	}
}

func TestCLISeparateLeadingDashContract(t *testing.T) {
	for _, option := range newDescription().CLISchema.Options {
		if option.Type == "boolean" {
			continue
		}
		values := []string{"-", "-h", "--unknown"}
		if option.Type == "duration" {
			values = append(values, "-1s")
		}
		if option.Type == "fd" {
			values = append(values, "-1")
		}
		for _, value := range values {
			for _, equals := range []bool{false, true} {
				args := []string{option.Name, value}
				if equals {
					args = []string{option.Name + "=" + value}
				}
				args = withCLIPrerequisites(option.Name, args)
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					_, _, err := parseArgs(args)
					want := ""
					if !equals && (option.Type == "string" || option.Type == "command" || strings.HasPrefix(value, "--")) {
						want = "missing value for " + option.Name
					} else {
						switch option.Type {
						case "duration":
							want = fmt.Sprintf("invalid duration for %s: time: invalid duration %q", option.Name, value)
							if value == "-1s" {
								want = option.Name + " must be greater than zero"
							}
						case "fd":
							want = fmt.Sprintf("invalid file descriptor for --events-fd: strconv.Atoi: parsing %q: invalid syntax", value)
							if value == "-1" {
								want = "--events-fd must be at least 3"
							}
						}
					}
					if want == "" {
						if err != nil {
							t.Fatalf("parseArgs(%q): %v", args, err)
						}
					} else if err == nil || err.Error() != want {
						t.Fatalf("parseArgs(%q) error = %v, want %q", args, err, want)
					}
				})
			}
		}
	}
}

func TestCLIOptionConstraints(t *testing.T) {
	for _, option := range newDescription().CLISchema.Options {
		t.Run(option.Name, func(t *testing.T) {
			for _, requirement := range option.Requires {
				_, _, err := parseArgs([]string{option.Name + "=echo ready"})
				want := option.Name + " requires " + requirement
				if err == nil || err.Error() != want {
					t.Errorf("missing requirement error = %v, want %q", err, want)
				}
			}
			for _, conflict := range option.Conflicts {
				args := []string{option.Name, conflict}
				want := option.Name + " cannot be combined with other arguments"
				if option.Type == "command" {
					args = []string{option.Name + "=echo ready", conflict + "=echo ready", "--idle=1s"}
					pair := strings.TrimSuffix(option.Name, ".async")
					want = pair + " and " + pair + ".async are mutually exclusive"
				}
				_, _, err := parseArgs(args)
				if err == nil || err.Error() != want {
					t.Errorf("parseArgs(%q) error = %v, want %q", args, err, want)
				}
			}
		})
	}
}
