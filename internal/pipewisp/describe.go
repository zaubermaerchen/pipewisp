package pipewisp

// This file defines and renders pipewisp's machine-readable CLI description.

import (
	"encoding/json"
	"io"
)

type description struct {
	SchemaVersion   int               `json:"schema_version"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	CLISchema       cliDescription    `json:"cli_schema"`
	StreamSemantics streamDescription `json:"stream_semantics"`
	StateMachine    stateDescription  `json:"state_machine"`
	SideEffects     []sideEffect      `json:"side_effects"`
}

type cliDescription struct {
	Options []cliOptionDescription `json:"options"`
}

type cliOptionDescription struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Repeatable  bool     `json:"repeatable"`
	Aliases     []string `json:"aliases,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	RequiresAny []string `json:"requires_any,omitempty"`
	Conflicts   []string `json:"conflicts,omitempty"`
}

type streamDescription struct {
	Stdin   streamInterfaceDescription `json:"stdin"`
	Stdout  streamInterfaceDescription `json:"stdout"`
	Stderr  streamInterfaceDescription `json:"stderr"`
	EventFD streamInterfaceDescription `json:"event_fd"`
}

type streamInterfaceDescription struct {
	Role        string `json:"role"`
	Description string `json:"description"`
	Format      string `json:"format,omitempty"`
	Option      string `json:"option,omitempty"`
}

type stateDescription struct {
	InitialState string            `json:"initial_state"`
	InitialEvent string            `json:"initial_event"`
	States       []string          `json:"states"`
	Events       []string          `json:"events"`
	Transitions  []stateTransition `json:"transitions"`
}

type stateTransition struct {
	From  string `json:"from"`
	Event string `json:"event"`
	To    string `json:"to"`
}

type sideEffect struct {
	Type            string `json:"type"`
	Trigger         string `json:"trigger"`
	Option          string `json:"option"`
	Async           bool   `json:"async,omitempty"`
	DryRunSupported bool   `json:"dry_run_supported"`
}

var descriptionOptionConflicts = []string{
	"--describe",
	"--help",
	"--version",
	"--name",
	"--events-fd",
	"--dry-run",
	"--verbose",
	"--on-ready",
	"--on-first-data",
	"--on-shutdown",
	"--idle",
	"--on-idle",
	"--on-idle.async",
	"--on-resume",
	"--on-resume.async",
	"--hook-timeout",
	"--ignore-hook-errors",
}

func newDescription() description {
	return description{
		SchemaVersion: 1,
		Name:          "pipewisp",
		Version:       currentVersion(),
		CLISchema: cliDescription{
			Options: []cliOptionDescription{
				{Name: "--describe", Type: "boolean", Conflicts: descriptionOptionConflictsWithout("--describe")},
				{Name: "--help", Type: "boolean", Aliases: []string{"-h"}, Conflicts: descriptionOptionConflictsWithout("--help")},
				{Name: "--version", Type: "boolean", Conflicts: descriptionOptionConflictsWithout("--version")},
				{Name: "--name", Type: "string"},
				{Name: "--events-fd", Type: "fd"},
				{Name: "--dry-run", Type: "boolean"},
				{Name: "--verbose", Type: "boolean"},
				{Name: "--on-ready", Type: "command"},
				{Name: "--on-first-data", Type: "command"},
				{Name: "--on-shutdown", Type: "command"},
				{
					Name:        "--idle",
					Type:        "duration",
					RequiresAny: []string{"--verbose", "--events-fd", "--on-idle", "--on-idle.async", "--on-resume", "--on-resume.async"},
				},
				{
					Name:      "--on-idle",
					Type:      "command",
					Requires:  []string{"--idle"},
					Conflicts: []string{"--on-idle.async"},
				},
				{
					Name:      "--on-idle.async",
					Type:      "command",
					Requires:  []string{"--idle"},
					Conflicts: []string{"--on-idle"},
				},
				{
					Name:      "--on-resume",
					Type:      "command",
					Requires:  []string{"--idle"},
					Conflicts: []string{"--on-resume.async"},
				},
				{
					Name:      "--on-resume.async",
					Type:      "command",
					Requires:  []string{"--idle"},
					Conflicts: []string{"--on-resume"},
				},
				{Name: "--hook-timeout", Type: "duration"},
				{Name: "--ignore-hook-errors", Type: "boolean"},
			},
		},
		StreamSemantics: streamDescription{
			Stdin: streamInterfaceDescription{
				Role:        "input",
				Description: "Primary input stream read from stdin.",
			},
			Stdout: streamInterfaceDescription{
				Role:        "passthrough",
				Description: "Primary output stream containing the original input bytes byte-for-byte and in order.",
			},
			Stderr: streamInterfaceDescription{
				Role:        "diagnostics",
				Description: "Diagnostics, warnings, errors, and hook output; it is separate from the passthrough data stream.",
			},
			EventFD: streamInterfaceDescription{
				Role:        "observation",
				Description: "Optional machine-readable lifecycle event stream written as JSONL when --events-fd is configured.",
				Format:      "jsonl",
				Option:      "--events-fd",
			},
		},
		StateMachine: stateDescription{
			InitialState: "ready",
			InitialEvent: "ready",
			States:       []string{"ready", "active", "idle", "shutdown"},
			Events:       []string{"ready", "first-data", "idle", "resume", "shutdown"},
			Transitions: []stateTransition{
				{From: "ready", Event: "first-data", To: "active"},
				{From: "ready", Event: "shutdown", To: "shutdown"},
				{From: "active", Event: "idle", To: "idle"},
				{From: "active", Event: "shutdown", To: "shutdown"},
				{From: "idle", Event: "resume", To: "active"},
				{From: "idle", Event: "shutdown", To: "shutdown"},
			},
		},
		SideEffects: []sideEffect{
			{Type: "execute-command", Trigger: "ready", Option: "--on-ready", DryRunSupported: true},
			{Type: "execute-command", Trigger: "first-data", Option: "--on-first-data", DryRunSupported: true},
			{Type: "execute-command", Trigger: "shutdown", Option: "--on-shutdown", DryRunSupported: true},
			{Type: "execute-command", Trigger: "idle", Option: "--on-idle", DryRunSupported: true},
			{Type: "execute-command", Trigger: "idle", Option: "--on-idle.async", Async: true, DryRunSupported: true},
			{Type: "execute-command", Trigger: "resume", Option: "--on-resume", DryRunSupported: true},
			{Type: "execute-command", Trigger: "resume", Option: "--on-resume.async", Async: true, DryRunSupported: true},
		},
	}
}

func descriptionOptionConflictsWithout(option string) []string {
	conflicts := make([]string, 0, len(descriptionOptionConflicts)-1)
	for _, candidate := range descriptionOptionConflicts {
		if candidate != option {
			conflicts = append(conflicts, candidate)
		}
	}
	return conflicts
}

func printDescription(out io.Writer) error {
	encoder := json.NewEncoder(out)
	return encoder.Encode(newDescription())
}
