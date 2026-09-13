package pipewisp

// This file verifies the machine-readable self-description contract.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseDescribe(t *testing.T) {
	got, help, err := parseArgs([]string{"--describe"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if !got.showDescription {
		t.Fatalf("parseArgs() showDescription = false, want true: %#v", got)
	}

	for _, args := range [][]string{
		{"--describe", "--verbose"},
		{"--verbose", "--describe"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, help, err := parseArgs(args)
			if err == nil {
				t.Fatal("parseArgs() error = nil, want error")
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if !strings.Contains(err.Error(), "--describe cannot be combined") {
				t.Fatalf("parseArgs() error = %q, want --describe combination error", err)
			}
		})
	}
}

func TestRunCLIDescribe(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := Run([]string{"--describe"}, panicReader{}, &output, &diagnostics); got != 0 {
		t.Fatalf("Run() exit code = %d, want 0", got)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("Run() diagnostics = %q, want empty", diagnostics.String())
	}

	var description map[string]json.RawMessage
	decoder := json.NewDecoder(&output)
	if err := decoder.Decode(&description); err != nil {
		t.Fatalf("decode --describe output: %v; output = %q", err, output.String())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("--describe output has trailing JSON/data: %v; output = %q", err, output.String())
	}

	wantFields := []string{
		"schema_version",
		"name",
		"version",
		"cli_schema",
		"stream_semantics",
		"state_machine",
		"side_effects",
	}
	if len(description) != len(wantFields) {
		t.Fatalf("--describe top-level fields = %d, want %d: %#v", len(description), len(wantFields), description)
	}
	for _, field := range wantFields {
		if _, ok := description[field]; !ok {
			t.Errorf("--describe output missing top-level field %q", field)
		}
	}

	var schemaVersion int
	if err := json.Unmarshal(description["schema_version"], &schemaVersion); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if schemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", schemaVersion)
	}
	var name string
	if err := json.Unmarshal(description["name"], &name); err != nil {
		t.Fatalf("name: %v", err)
	}
	if name != "pipewisp" {
		t.Fatalf("name = %q, want pipewisp", name)
	}
	var versionValue string
	if err := json.Unmarshal(description["version"], &versionValue); err != nil {
		t.Fatalf("version: %v", err)
	}
	if versionValue != currentVersion() {
		t.Fatalf("version = %q, want currentVersion() = %q", versionValue, currentVersion())
	}
}

func TestRunCLIDescribeReportsOutputError(t *testing.T) {
	wantErr := "description output unavailable"
	var diagnostics bytes.Buffer

	if got := Run([]string{"--describe"}, panicReader{}, errorWriter{err: errors.New(wantErr)}, &diagnostics); got != 1 {
		t.Fatalf("Run() exit code = %d, want 1", got)
	}
	if got, want := diagnostics.String(), "pipewisp: "+wantErr+"\n"; got != want {
		t.Fatalf("Run() diagnostics = %q, want %q", got, want)
	}
}

func TestPublicDescriptionMetadataIsComplete(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	if got := Run([]string{"--describe"}, panicReader{}, &output, &diagnostics); got != 0 {
		t.Fatalf("Run() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}

	var document struct {
		CLISchema struct {
			Options []map[string]json.RawMessage `json:"options"`
		} `json:"cli_schema"`
		StreamSemantics map[string]publicStreamInterface `json:"stream_semantics"`
		StateMachine    struct {
			InitialState string             `json:"initial_state"`
			States       []string           `json:"states"`
			Events       []string           `json:"events"`
			Transitions  []publicTransition `json:"transitions"`
		} `json:"state_machine"`
		SideEffects []publicSideEffect `json:"side_effects"`
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode --describe output: %v; output = %q", err, output.String())
	}

	wantTypes := map[string]string{
		"--describe":           "boolean",
		"--help":               "boolean",
		"--version":            "boolean",
		"--name":               "string",
		"--events-fd":          "fd",
		"--verbose":            "boolean",
		"--on-ready":           "command",
		"--on-first-data":      "command",
		"--on-shutdown":        "command",
		"--idle":               "duration",
		"--on-idle":            "command",
		"--on-idle.async":      "command",
		"--on-resume":          "command",
		"--on-resume.async":    "command",
		"--hook-timeout":       "duration",
		"--ignore-hook-errors": "boolean",
	}
	wantIdleRequirements := []string{"--verbose", "--events-fd", "--on-idle", "--on-idle.async", "--on-resume", "--on-resume.async"}
	wantConflicts := map[string][]string{
		"--on-idle":         {"--on-idle.async"},
		"--on-idle.async":   {"--on-idle"},
		"--on-resume":       {"--on-resume.async"},
		"--on-resume.async": {"--on-resume"},
	}
	seen := make(map[string]bool, len(document.CLISchema.Options))
	for _, option := range document.CLISchema.Options {
		name, err := rawString(option, "name")
		if err != nil {
			t.Fatalf("option name: %v", err)
		}
		if seen[name] {
			t.Fatalf("duplicate option %q", name)
		}
		seen[name] = true
		wantType, ok := wantTypes[name]
		if !ok {
			t.Fatalf("unexpected option %q", name)
		}
		if got, err := rawString(option, "type"); err != nil || got != wantType {
			t.Fatalf("option %s type = %q, error = %v, want %q", name, got, err, wantType)
		}
		for _, field := range []string{"name", "type", "required", "repeatable"} {
			if _, ok := option[field]; !ok {
				t.Errorf("option %s missing required schema field %q", name, field)
			}
		}
		if got, err := rawBool(option, "required"); err != nil || got {
			t.Errorf("option %s required = %v, error = %v, want false", name, got, err)
		}
		if got, err := rawBool(option, "repeatable"); err != nil || got {
			t.Errorf("option %s repeatable = %v, error = %v, want false", name, got, err)
		}
		if name == "--help" {
			if got := rawStrings(t, option, "aliases"); !equalStrings(got, []string{"-h"}) {
				t.Errorf("--help aliases = %#v, want [-h]", got)
			}
		}
		if name == "--idle" {
			if got := rawStrings(t, option, "requires_any"); !equalStrings(got, wantIdleRequirements) {
				t.Errorf("--idle requires_any = %#v, want %#v", got, wantIdleRequirements)
			}
		}
		if want, ok := wantConflicts[name]; ok {
			if got := rawStrings(t, option, "conflicts"); !equalStrings(got, want) {
				t.Errorf("%s conflicts = %#v, want %#v", name, got, want)
			}
		}
		if strings.HasPrefix(name, "--on-idle") || strings.HasPrefix(name, "--on-resume") {
			if got := rawStrings(t, option, "requires"); !equalStrings(got, []string{"--idle"}) {
				t.Errorf("%s requires = %#v, want [--idle]", name, got)
			}
		}
	}
	if len(seen) != len(wantTypes) {
		t.Fatalf("option count = %d, want %d; seen = %#v", len(seen), len(wantTypes), seen)
	}
	for name := range wantTypes {
		if !seen[name] {
			t.Errorf("missing option %q", name)
		}
	}

	wantRoles := map[string]string{
		"stdin":    "input",
		"stdout":   "passthrough",
		"stderr":   "diagnostics",
		"event_fd": "observation",
	}
	if len(document.StreamSemantics) != len(wantRoles) {
		t.Fatalf("stream interface count = %d, want %d: %#v", len(document.StreamSemantics), len(wantRoles), document.StreamSemantics)
	}
	for name, wantRole := range wantRoles {
		interfaceDescription, ok := document.StreamSemantics[name]
		if !ok {
			t.Fatalf("missing stream interface %q", name)
		}
		if interfaceDescription.Role != wantRole {
			t.Errorf("stream interface %s role = %q, want %q", name, interfaceDescription.Role, wantRole)
		}
		if strings.TrimSpace(interfaceDescription.Description) == "" {
			t.Errorf("stream interface %s description is empty", name)
		}
	}
	eventFD := document.StreamSemantics["event_fd"]
	if eventFD.Format != "jsonl" {
		t.Errorf("event_fd format = %q, want jsonl", eventFD.Format)
	}
	if eventFD.Option != "--events-fd" {
		t.Errorf("event_fd option = %q, want --events-fd", eventFD.Option)
	}
	stdoutDescription := strings.ToLower(document.StreamSemantics["stdout"].Description)
	if !strings.Contains(stdoutDescription, "byte-for-byte") || !strings.Contains(stdoutDescription, "order") {
		t.Errorf("stdout description = %q, want byte-for-byte ordered data-path semantics", document.StreamSemantics["stdout"].Description)
	}

	wantTransitions := []publicTransition{
		{From: "ready", Event: "first-data", To: "active"},
		{From: "ready", Event: "shutdown", To: "shutdown"},
		{From: "active", Event: "idle", To: "idle"},
		{From: "active", Event: "shutdown", To: "shutdown"},
		{From: "idle", Event: "resume", To: "active"},
		{From: "idle", Event: "shutdown", To: "shutdown"},
	}
	if document.StateMachine.InitialState != "ready" || !equalStrings(document.StateMachine.States, []string{"ready", "active", "idle", "shutdown"}) {
		t.Fatalf("state machine initial/states = %q/%#v, want ready/[ready active idle shutdown]", document.StateMachine.InitialState, document.StateMachine.States)
	}
	if !equalStrings(document.StateMachine.Events, []string{"ready", "first-data", "idle", "resume", "shutdown"}) {
		t.Fatalf("state machine events = %#v, want [ready first-data idle resume shutdown]", document.StateMachine.Events)
	}
	if len(document.StateMachine.Transitions) != len(wantTransitions) {
		t.Fatalf("transition count = %d, want %d", len(document.StateMachine.Transitions), len(wantTransitions))
	}
	for i, want := range wantTransitions {
		if document.StateMachine.Transitions[i] != want {
			t.Errorf("transition %d = %#v, want %#v", i, document.StateMachine.Transitions[i], want)
		}
	}

	wantTriggers := map[string]string{
		"--on-ready":        "ready",
		"--on-first-data":   "first-data",
		"--on-shutdown":     "shutdown",
		"--on-idle":         "idle",
		"--on-idle.async":   "idle",
		"--on-resume":       "resume",
		"--on-resume.async": "resume",
	}
	if len(document.SideEffects) != len(wantTriggers) {
		t.Fatalf("side effect count = %d, want %d", len(document.SideEffects), len(wantTriggers))
	}
	seenEffects := make(map[string]bool, len(document.SideEffects))
	for _, effect := range document.SideEffects {
		if effect.Type != "execute-command" {
			t.Errorf("side effect %s type = %q, want execute-command", effect.Option, effect.Type)
		}
		if effect.Trigger != wantTriggers[effect.Option] {
			t.Errorf("side effect %s trigger = %q, want %q", effect.Option, effect.Trigger, wantTriggers[effect.Option])
		}
		if seenEffects[effect.Option] {
			t.Errorf("duplicate side effect option %q", effect.Option)
		}
		seenEffects[effect.Option] = true
		if strings.HasSuffix(effect.Option, ".async") != effect.Async {
			t.Errorf("side effect %s async = %v, want suffix match", effect.Option, effect.Async)
		}
	}
	for option := range wantTriggers {
		if !seenEffects[option] {
			t.Errorf("missing side effect option %q", option)
		}
	}
	if seenEffects["--events-fd"] {
		t.Fatal("--events-fd unexpectedly appears in side effects")
	}
}

type publicTransition struct {
	From  string `json:"from"`
	Event string `json:"event"`
	To    string `json:"to"`
}

type publicStreamInterface struct {
	Role        string `json:"role"`
	Description string `json:"description"`
	Format      string `json:"format"`
	Option      string `json:"option"`
}

type publicSideEffect struct {
	Type    string `json:"type"`
	Trigger string `json:"trigger"`
	Option  string `json:"option"`
	Async   bool   `json:"async"`
}

func rawString(fields map[string]json.RawMessage, name string) (string, error) {
	var value string
	raw, ok := fields[name]
	if !ok {
		return "", errors.New("missing field " + name)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawBool(fields map[string]json.RawMessage, name string) (bool, error) {
	var value bool
	raw, ok := fields[name]
	if !ok {
		return false, errors.New("missing field " + name)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, err
	}
	return value, nil
}

func rawStrings(t *testing.T, fields map[string]json.RawMessage, name string) []string {
	t.Helper()
	var value []string
	raw, ok := fields[name]
	if !ok {
		t.Fatalf("missing field %s", name)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode field %s: %v", name, err)
	}
	return value
}

func TestDescriptionCLIAndLifecycleMetadata(t *testing.T) {
	description := newDescription()

	if got, want := len(description.CLISchema.Options), 16; got != want {
		t.Fatalf("CLI option count = %d, want %d", got, want)
	}
	wantTypes := map[string]string{
		"--describe":           "boolean",
		"--help":               "boolean",
		"--version":            "boolean",
		"--name":               "string",
		"--events-fd":          "fd",
		"--verbose":            "boolean",
		"--on-ready":           "command",
		"--on-first-data":      "command",
		"--on-shutdown":        "command",
		"--idle":               "duration",
		"--on-idle":            "command",
		"--on-idle.async":      "command",
		"--on-resume":          "command",
		"--on-resume.async":    "command",
		"--hook-timeout":       "duration",
		"--ignore-hook-errors": "boolean",
	}
	for _, option := range description.CLISchema.Options {
		wantType, ok := wantTypes[option.Name]
		if !ok {
			t.Errorf("unexpected CLI option %q", option.Name)
			continue
		}
		if option.Type != wantType {
			t.Errorf("option %s type = %q, want %q", option.Name, option.Type, wantType)
		}
		if option.Required {
			t.Errorf("option %s required = true, want false", option.Name)
		}
		if option.Repeatable {
			t.Errorf("option %s repeatable = true, want false", option.Name)
		}
	}

	if got, want := description.StateMachine.InitialState, "ready"; got != want {
		t.Fatalf("initial state = %q, want %q", got, want)
	}
	if got, want := description.StateMachine.States, []string{"ready", "active", "idle", "shutdown"}; !equalStrings(got, want) {
		t.Fatalf("states = %#v, want %#v", got, want)
	}
	if !containsString(description.StateMachine.Events, "first-data") {
		t.Fatalf("lifecycle events = %#v, want first-data", description.StateMachine.Events)
	}
	if containsString(description.StateMachine.States, "first-data") {
		t.Fatalf("first-data is incorrectly listed as a state: %#v", description.StateMachine.States)
	}
}

func TestDescriptionSideEffects(t *testing.T) {
	description := newDescription()
	want := map[string]bool{
		"--on-ready":        false,
		"--on-first-data":   false,
		"--on-shutdown":     false,
		"--on-idle":         false,
		"--on-idle.async":   true,
		"--on-resume":       false,
		"--on-resume.async": true,
	}
	if got, wantCount := len(description.SideEffects), len(want); got != wantCount {
		t.Fatalf("side effect count = %d, want %d", got, wantCount)
	}
	for _, effect := range description.SideEffects {
		async, ok := want[effect.Option]
		if !ok {
			t.Errorf("unexpected side effect option %q", effect.Option)
			continue
		}
		if effect.Type != "execute-command" {
			t.Errorf("side effect %s type = %q, want execute-command", effect.Option, effect.Type)
		}
		if effect.Async != async {
			t.Errorf("side effect %s async = %v, want %v", effect.Option, effect.Async, async)
		}
		if effect.Trigger == "" {
			t.Errorf("side effect %s trigger is empty", effect.Option)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
