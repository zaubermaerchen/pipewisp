package pipewisp

// This file shares descriptive option metadata between help and the CLI schema.

// Slice order defines schema order and the order within each help usage line.
// It deliberately carries no parser callbacks or stream/lifecycle metadata.
type cliOption struct {
	cliOptionDescription
	metavar   string
	help      bool
	exclusive bool
}

var cliOptions = []cliOption{
	{cliOptionDescription: cliOptionDescription{Name: "--describe", Type: "boolean"}, help: true, exclusive: true},
	{cliOptionDescription: cliOptionDescription{Name: "--help", Type: "boolean", Aliases: []string{"-h"}}, exclusive: true},
	{cliOptionDescription: cliOptionDescription{Name: "--version", Type: "boolean"}, help: true, exclusive: true},
	{cliOptionDescription: cliOptionDescription{Name: "--name", Type: "string"}, metavar: "NAME", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--events-fd", Type: "fd"}, metavar: "FD", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--dry-run", Type: "boolean"}, help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--verbose", Type: "boolean"}, help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--on-ready", Type: "command"}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--on-first-data", Type: "command"}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--on-shutdown", Type: "command"}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{
		Name: "--idle", Type: "duration",
		RequiresAny: []string{"--verbose", "--events-fd", "--on-idle", "--on-idle.async", "--on-resume", "--on-resume.async"},
	}, metavar: "DURATION", help: true},
	{cliOptionDescription: cliOptionDescription{
		Name: "--on-idle", Type: "command", Requires: []string{"--idle"}, Conflicts: []string{"--on-idle.async"},
	}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{
		Name: "--on-idle.async", Type: "command", Requires: []string{"--idle"}, Conflicts: []string{"--on-idle"},
	}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{
		Name: "--on-resume", Type: "command", Requires: []string{"--idle"}, Conflicts: []string{"--on-resume.async"},
	}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{
		Name: "--on-resume.async", Type: "command", Requires: []string{"--idle"}, Conflicts: []string{"--on-resume"},
	}, metavar: "COMMAND", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--hook-timeout", Type: "duration"}, metavar: "DURATION", help: true},
	{cliOptionDescription: cliOptionDescription{Name: "--ignore-hook-errors", Type: "boolean"}, help: true},
}

func describeCLIOptions() []cliOptionDescription {
	options := make([]cliOptionDescription, 0, len(cliOptions))
	for _, definition := range cliOptions {
		option := definition.cliOptionDescription
		if definition.exclusive {
			option.Conflicts = make([]string, 0, len(cliOptions)-1)
			for _, other := range cliOptions {
				if other.Name != option.Name {
					option.Conflicts = append(option.Conflicts, other.Name)
				}
			}
		}
		options = append(options, option)
	}
	return options
}
