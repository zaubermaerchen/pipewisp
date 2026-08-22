# pipewisp

`pipewisp` passes standard input to standard output byte-for-byte while running
optional commands at the beginning and end of the stream lifecycle. It is
intended for inserting synchronous side effects into a Unix or Windows pipe.

## Install and build

From a checkout:

```sh
go install ./cmd/pipewisp
go build -o pipewisp ./cmd/pipewisp
```

The module can also be installed directly once published:

```sh
go install github.com/zaubermaerchen/pipewisp/cmd/pipewisp@latest
```

## Download releases

Tagged binaries are available from the repository's
[GitHub Releases](https://github.com/zaubermaerchen/pipewisp/releases). Each
release provides these six archives: `linux_amd64`, `linux_arm64`,
`darwin_amd64`, `darwin_arm64`, `windows_amd64`, and `windows_arm64`.

Download the archive for your platform and verify it with the accompanying
checksums file before extracting it. For example, on Linux:

```sh
archive=pipewisp_v0.1.0_linux_amd64.tar.gz
grep -F -- "  $archive" SHA256SUMS > "$archive.sha256" || exit 1
sha256sum -c "$archive.sha256"
```

On macOS, replace the final command with
`shasum -a 256 -c "$archive.sha256"`. On Windows
PowerShell:

```powershell
$archive = "pipewisp_v0.1.0_windows_amd64.zip"
$expected = (Get-Content SHA256SUMS | Where-Object { $_ -like "*  $archive" }).Split()[0]
$actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "checksum mismatch: $archive" }
```

## Usage

```text
pipewisp [--on COMMAND] [--off COMMAND]
```

Each of `--on` and `--off` is optional and may be specified at most once.
The separated and equals forms are supported:

```sh
producer | pipewisp
producer | pipewisp --on 'printf "started\\n"' --off 'printf "stopped\\n"'
producer | pipewisp --on='prepare' --off='cleanup'
pipewisp --help
```

Duplicate options, unknown options, missing or empty commands, and positional
arguments are errors. `--help` prints usage and exits successfully.

Commands are executed synchronously in this order:

1. `--on`, if present
2. the unmodified stdin-to-stdout copy
3. `--off`, if present, after EOF, an I/O error, or a handled signal

On Unix, hooks run as `/bin/sh -c COMMAND`. On Windows, they run as
`cmd.exe /C COMMAND`. Hook stdin is isolated from the passthrough stream: it
is connected to an empty/null input. Hook stdout and stderr are both sent to
pipewisp's stderr; stdout contains passthrough data only.

Both hook options execute their command through a shell. Do not pass
untrusted or unsanitized command strings: shell metacharacters have their
normal platform-specific meaning.

## Exit status

| Cause | Status | Behavior |
| --- | ---: | --- |
| Normal EOF | 0 | Run `--off`, if present. |
| Downstream broken pipe (EPIPE) | 0 | Treat downstream closure as normal; run `--off` and suppress the broken-pipe diagnostic. |
| Other copy/I/O error | 1 | Report the error and run `--off`, if present. |
| `--on` failure | 1 | Report the hook failure; do not copy data or run `--off`. |
| `--off` failure | 1 | Report the hook failure. |
| SIGINT / Ctrl+C | 130 | Run `--off` synchronously; this signal status wins if `--off` also fails. |
| SIGTERM (Unix) | 143 | Run `--off` synchronously; this signal status wins if `--off` also fails. |

On Unix, pipewisp ignores SIGPIPE so a closed downstream is reported as an
EPIPE copy result instead of terminating the process before cleanup can run.
EPIPE only describes pipewisp's downstream; it cannot keep a consumer alive,
reopen a closed pipe, or determine how an upstream producer reacts.

## v0.1 scope and cleanup limits

The v0.1 interface intentionally has no configuration files, multiple hooks,
hook timeouts, idle/resume mode, verbose mode, or option to ignore hook
failures. Cleanup cannot run after SIGKILL, an unrecoverable process crash, or
power loss, because those conditions do not allow a user-space hook to execute.
