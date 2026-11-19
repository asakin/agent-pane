# RFC 0002: Windows support

- **Status:** Proposed
- **Issue:** #47
- **Author:** Tessl, for @asakin

## Summary

Ship Agent Pane on Windows x86_64 with the same product shape as on macOS and Linux: the same extension, a native host installed for the Chromium browsers Agent Pane supports, and pane tabs that run your shell and coding agents. A PowerShell installer published with each release installs a Windows release archive into `%LOCALAPPDATA%\agent-pane\`, registers the native-messaging manifest under each browser's `HKCU\Software\...\NativeMessagingHosts` key, and puts `agent-pane.exe` on the user's `PATH`. The placing step is a Go subcommand rather than a PowerShell port of `place.sh`, so the installer and `agent-pane doctor` share one description of the install. The bundled herdr-core becomes a multi-file build (`hc.exe` plus its ConPTY files), each file pinned by checksum in `scripts/herdr-core.lock`. The Built-in backend stays the default on Windows as on the other platforms, and Durable (preview) is offered once the core client speaks named pipes. Windows ships first **without page access**: `agent-pane page` refuses with a clear message, as `page_windows.go` does today, and a later, separately reviewed stage adds a named-pipe page channel with a Windows peer check. A `windows-latest` CI job comes first, and a Windows release smoke test comes with the installer. The work is split into ten implementation issues, in order, in [Rollout](#rollout).

## Motivation

Agent Pane runs on macOS and Linux only. Windows users of Chrome, Edge and Brave who run Claude Code, Codex or other coding agents on Windows (not inside WSL) cannot use it. #18 asks for Windows support and lists a scope; RFC 0001 lists Windows only under Future possibilities. `rfcs/README.md` calls for an RFC because Windows support changes what the installer puts on a machine, how Agent Pane is distributed, and the page-access trust boundary.

What the repository has today:

- **Build.** `GOOS=windows GOARCH=amd64 go build .` succeeds: the Windows files `start_windows.go` (go-pty's ConPTY), `page_windows.go` (refuses page reads) and `lock_windows.go` (no file locks) are stubs that compile. `go vet ./...` for Windows fails in test code (`team_test.go` uses `eventually`, which is defined in a unix-only test file). Nothing is tested on Windows.
- **Installer.** `install.sh` and `scripts/place.sh` are POSIX sh and refuse any OS but Darwin and Linux. The host is a sh wrapper (`native_messaging_host`) that runs `agent-pane serve`. Manifests are JSON files written into each browser's config folder. On Windows, Chromium browsers find a host through a registry key whose default value is the path of the manifest file.
- **Release.** `.goreleaser.yaml` builds `linux` and `darwin` only (it already has a `zip` override for `windows`). `release.yml` and its smoke test run on `ubuntu-latest` and `macos-latest`.
- **Bundled core.** herdr-core 0.1.19 publishes `herdr-core-windows-x86_64.zip` (SHA-256 `1cf7de14...b813a`, in its `manifest.json`). It holds `herdr-core.exe`, `conpty/conpty.dll`, `conpty/x64/OpenConsole.exe`, `LICENSE`, and a `THIRD-PARTY-NOTICES/` folder of three files, where the Unix archives have one `THIRD-PARTY-NOTICES` file. `scripts/herdr-core.lock` and `scripts/fetch-herdr-core.sh` handle one `hc` binary from a `.tar.gz`.
- **Core transport.** herdr-core listens on a named pipe on Windows, not a unix socket. Its security notes say the pipe's access rules allow the current account and SYSTEM, the name includes the account and session, and state is under `%LOCALAPPDATA%\herdr-core\<session>\` unless `XDG_STATE_HOME` is set. `internal/herdrcore` dials `unix` only.
- **Page access.** Page reads go over an inherited socketpair (`AGENT_PANE_FD`) or a per-session unix socket (`AGENT_PANE_SOCKET`) admitted by `internal/peercred`: same uid, and the session's root pid or a descendant. `peercred` has no Windows implementation, and `Policy.Allow` compares against `os.Getuid()`, which is `-1` on Windows.
- **Other platform code.** The default shell comes from `$SHELL` (`getDefaultShell` in `root.go`, and `agents.go`), with `/bin/sh` as the fallback. Paths are built from `$HOME` (`configDir` in `root.go`, `layoutFor` in `doctor.go`). `agent-pane status` writes to `/dev/tty`. `hardenHost` is a no-op outside Linux.

The outcome this RFC aims for: a person on Windows runs one PowerShell command, loads the extension, opens the side panel in Chrome or Edge, and starts Claude Code in a pane tab in their project folder; `agent-pane doctor` says the install is good; and they can tell from the README which features are not on Windows yet.

## Guide-level explanation

### For a person using Agent Pane on Windows

Install from PowerShell:

```powershell
irm https://github.com/asakin/agent-pane/releases/latest/download/install.ps1 | iex
```

Then load the extension from `%LOCALAPPDATA%\agent-pane\extensions\chrome` in `chrome://extensions` (or `edge://extensions`) the same way as on the other platforms. Running the command again updates; `agent-pane doctor` and `agent-pane doctor --fix` work as they do elsewhere.

What is the same: pane tabs, the launcher and agent definitions, Options, themes, Send tab, blocked domains, the agent skill, version checks between the extension and the program.

What differs, at first:

- **Shell.** With no shell set in Options, a pane tab starts PowerShell 7 (`pwsh.exe`) when it is on `PATH`, else Windows PowerShell (`powershell.exe`). This matches herdr-core's choice. Any shell can be set in Options, including Git Bash or `cmd.exe`.
- **Agents.** The launcher finds agents through `PATH` and `PATHEXT`, so npm's `claude.cmd` shims are found. An agent that only installs a `.ps1` script is not found until its definition names a command that is.
- **Page reads.** `agent-pane page` is not available on Windows in the first release and says so. Send tab still works, because it goes through the extension, not the program.
- **Keyboard.** The Linux bindings apply (Ctrl+Backspace, Alt+Left and so on).
- **Quitting the browser** ends Built-in pane tabs, as on Linux. Durable (preview), once available on Windows, behaves as on the other platforms, subject to the [prototype](#what-needs-a-prototype).

### For a contributor

A `go-windows` CI job runs `go vet`, `go test` and `go build` on `windows-latest`. Windows-only code sits in `_windows.go` files as today. The installer's placing logic for Windows is Go code under `internal/cmd/` with its own tests on the Windows runner, and `place.sh` keeps doing macOS and Linux unchanged.

## Reference-level explanation

### Which parts of #18 this RFC keeps, changes or drops

| #18 scope or criterion | Decision | Why |
| --- | --- | --- |
| Bundle and use Agent Pane's private herdr-core on Windows | **Keep**, as the Durable (preview) backend | The core is already shipped and checked on the other platforms; Windows gets the same, with a multi-file layout. |
| "Agent Pane uses the Herdr-core-backed terminal/session path on Windows" | **Change:** Built-in stays the default on Windows; Durable is offered, not forced | Durable is a preview everywhere. Making it the only backend on one platform would ship the least tested platform on the least tested backend with no fallback. RFC 0001's stage 6 decides the default for every platform together. |
| Native-messaging manifest for supported Chromium browsers | **Keep**, via registry keys | That is how Chromium browsers find hosts on Windows. |
| `agent-pane doctor` validates the install, the core, the manifests and host behavior | **Keep** | See [doctor](#agent-pane-doctor-on-windows). |
| Pane tabs, agent launchers and the CLI work | **Keep** | Built-in already compiles for Windows with ConPTY; the gaps are shell, agent lookup, paths and `status`. |
| Page-access features work | **Change:** a later stage with its own security review | A new trust boundary should not ride along with an installer. Shipping without it gives Windows users pane tabs sooner and keeps the review focused. |
| The side panel can launch a shell and supported agents | **Keep** | |
| Tests pass on Windows, or documented equivalent checks | **Keep**, as a `windows-latest` CI job and a release smoke test | |
| No separate user-installed Herdr app | **Keep** | |
| Windows ARM out of scope | **Keep** | herdr-core ships no Windows aarch64 build. |
| One issue for all of it | **Drop** | Split into the issues in [Rollout](#rollout), each one pull request. |

### Install layout

Everything goes under one per-user folder, with no administrator rights and no machine-wide registry keys:

| Today (macOS, Linux) | Windows |
| --- | --- |
| `~/.local/bin/agent-pane` | `%LOCALAPPDATA%\agent-pane\bin\agent-pane.exe`, its folder added to the user `Path` (`HKCU\Environment`) |
| `~/.local/share/agent-pane/` (release, extension, agents, skills, libexec, licenses, state) | `%LOCALAPPDATA%\agent-pane\` with the same subfolders |
| `~/.local/share/agent-pane/native_messaging_host` (sh wrapper) | None; the manifest points at `agent-pane.exe` (below) |
| Manifest JSON in each browser's config folder | `%LOCALAPPDATA%\agent-pane\com.agentpane.native.json`, named by each browser's registry key |
| `~/.config/agent-pane/` | `%APPDATA%\agent-pane\` (roaming, like herdr-core's config) |
| `~/.claude/skills/agent-pane/`, `~/.agents/skills/agent-pane/` | The same under `%USERPROFILE%`, where those agents look on Windows |
| macOS spawn LaunchAgent | None |

`root.go`'s `configDir` and `doctor.go`'s `layoutFor` gain a Windows branch; the rest of the code takes paths from them.

### Native-messaging registration

The host manifest is one file, with `path` set to the absolute path of `agent-pane.exe` and the same `allowed_origins` as today. For each supported browser that is installed for this user, the placing step sets the default value of:

| Browser | Key under `HKCU\Software\` |
| --- | --- |
| Google Chrome | `Google\Chrome\NativeMessagingHosts\com.agentpane.native` |
| Microsoft Edge | `Microsoft\Edge\NativeMessagingHosts\com.agentpane.native` |
| Chromium | `Chromium\NativeMessagingHosts\com.agentpane.native` |
| Brave | `BraveSoftware\Brave-Browser\NativeMessagingHosts\com.agentpane.native` |
| Vivaldi, Helium | To confirm by prototype; see [Unresolved questions](#unresolved-questions) |

"Installed" is judged as today, by the browser's user-data folder existing (for example `%LOCALAPPDATA%\Google\Chrome\User Data`). The browser list stays in one Go table (`browserDirs` in `doctor.go`), which on Windows is used by both the placing step and doctor, removing one of today's "change both" pairs for this platform.

**How Chrome starts the host.** Chrome on Windows runs the manifest's `path` with the caller origin and `--parent-window=<handle>` as arguments, with stdin and stdout as pipes. Today the sh wrapper adds `serve`. On Windows the proposal is that `agent-pane.exe`, given a first argument that starts with `chrome-extension://`, runs `serve`; Chrome passes that argument on every platform, so the rule can be the same everywhere, but it is only needed on Windows. The alternative, a `.bat` wrapper, runs through `cmd.exe`, adds a console process between Chrome and the host, and quotes arguments through `cmd`'s rules; it is listed under [alternatives](#rationale-and-alternatives).

**Replacing a running binary.** Windows will not overwrite or delete an `.exe` that is running, and Chrome's host or the core may be running. The placing step renames the old file aside (`agent-pane.exe.old-<n>`), moves the new one in, and deletes old copies it can on the next run. The same applies to `hc.exe` and the ConPTY files, which a running core and its holders keep open.

### Installer and distribution

- **Archive.** goreleaser builds `windows/amd64` and the existing `zip` override names it `agent-pane-<version>-windows_amd64.zip`. It carries `agent-pane.exe`, `README.md`, `LICENSE`, `extensions/chrome/`, `agents/`, `skills/agent-pane/`, `instructions.md`, `herdr-core.lock`, `libexec/` (the core, below) and `licenses/herdr-core/`. It does not carry `place.sh` or shell completions; a PowerShell completion can come later. `checksums.txt` covers it like the other archives.
- **`install.ps1`.** A template in the repository, like `install.sh`, that refuses to run without a version; `release.yml` stamps the tag in and publishes it beside `install.sh`. It checks the machine is Windows x86_64, downloads the release's zip and `checksums.txt`, checks the zip with `Get-FileHash`, unpacks it to a temporary folder with `Expand-Archive`, and runs `agent-pane.exe place` from there. It does nothing else, so its logic stays as small as `install.sh`'s. It must work in Windows PowerShell 5.1, which every supported Windows has, and in PowerShell 7.
- **Placing step.** A hidden `agent-pane place` subcommand, built for Windows only at first, does what `place.sh` does on the other platforms: keep a copy of the release in `%LOCALAPPDATA%\agent-pane\release`, place the binary and the core, sync the extension, agents, skill and licenses, write the manifest and registry keys, add the user `Path` entry (and broadcast `WM_SETTINGCHANGE` so new terminals see it), upsert the Agent Pane block in `%USERPROFILE%\AGENTS.md` and `CLAUDE.md` as `place.sh` does, and end with `doctor`. Like `place.sh`, it compares before it writes and prints a line only for what it changed, so a second run prints nothing. `doctor --fix` runs it from the installed release copy. Registry writes use `golang.org/x/sys/windows/registry`, already a dependency through `golang.org/x/sys`.
- **Updates.** Run the install command again. Built-in pane tabs end when the host is replaced, because the browser restarts the host after the extension reloads, as on Linux. Durable pane tabs follow RFC 0001's upgrade path.
- **Uninstall.** A documented PowerShell snippet that removes the registry keys, the `Path` entry and the folder. An `agent-pane uninstall` command is a [future possibility](#future-possibilities).
- **Signing.** Release binaries for macOS are notarized. On Windows, an unsigned `agent-pane.exe` that a browser downloads gets a SmartScreen warning, and some managed machines block unsigned programs. Files that `Invoke-RestMethod` downloads are not normally marked as coming from the internet, so the install command is less likely to be blocked than a browser download; the smoke test should confirm it. herdr-core's own Windows builds are signed only when its release workflow has a signing profile. Whether Agent Pane signs its Windows binary with Authenticode before the first Windows release is an [unresolved question](#unresolved-questions).

### Bundled herdr-core on Windows

**Lock.** Each lock line today is `<goos>_<goarch> <archive url> <archive sha256> <herdr-core binary sha256>`. The Windows build has three executable files that must match each other. The lock gains `file` lines that pin each additional file of a platform's build, keeping the existing line format so the Unix entries and `fetch-herdr-core.sh`'s parsing of them do not change. For 0.1.19 the Windows entry would be:

```text
windows_amd64 https://releases.herdr.dev/core/0.1.19/herdr-core-windows-x86_64.zip 1cf7de14c862abc49b8eaeed32dc6a60d8146740edb2c244570d1a9f07cb813a 8c5c088eda5c441502fa58714277a250e38103c66180376ef00d63d6cab03f8d
windows_amd64.file conpty/conpty.dll 39fba2713e2495117b1591ae8c32a3b904bea7aa66069cf7815e2844c76d75d8
windows_amd64.file conpty/x64/OpenConsole.exe b7fd936c2668b87b9ecf7b3366dc6568afc1c6f981874cba3e955a1c35cf8160
```

**Fetch.** `fetch-herdr-core.sh` keeps running on the Ubuntu release runner and in dev installs. For a `.zip` URL it unpacks with `unzip`, checks `herdr-core.exe` and each `file` line, and lays out:

```text
<dir>/libexec/hc.exe
<dir>/libexec/conpty/conpty.dll
<dir>/libexec/conpty/x64/OpenConsole.exe
<dir>/licenses/herdr-core/LICENSE
<dir>/licenses/herdr-core/THIRD-PARTY-NOTICES/   (the archive's folder of notices)
<dir>/herdr-core.lock
```

The ConPTY files keep their path relative to the executable, because that is where herdr-core looks for them; the [prototype](#what-needs-a-prototype) confirms that renaming `herdr-core.exe` to `hc.exe` does not break that lookup, or the binary keeps its name and `layoutFor` names it. `place.sh`'s and doctor's check that `THIRD-PARTY-NOTICES` is a non-empty file becomes "a non-empty file or a folder of non-empty files" on Windows.

**Place.** The placing step installs the whole `libexec\` tree, never on `Path`, renaming running files aside as above.

**Check.** Doctor checks each pinned file's checksum, `hc.exe --version`, the licenses, and that no `hc.exe` on `Path` is Agent Pane's copy (the existing "core isolation" check, with `PATHEXT`).

**Client.** `internal/herdrcore` dials the core's named pipe on Windows instead of a unix socket. `Core.Socket` becomes the pipe name, set through `HC_SOCKET` as today so the private session is explicit. Go's standard library has no named-pipe client. The options are `github.com/Microsoft/go-winio` (a new, widely used dependency) or a small client on `golang.org/x/sys/windows` (`CreateFile` on `\\.\pipe\...`, overlapped I/O for deadlines); which one is an [unresolved question](#unresolved-questions). The framed protocol above the connection is unchanged.

**Other Durable gaps.** `lockFile` gets a Windows implementation with `LockFileEx`, since `panes.json` is shared by each browser's host. `Core.Env`'s `XDG_STATE_HOME` and `XDG_CONFIG_HOME` keep the core's state under `%LOCALAPPDATA%\agent-pane\core\`, which herdr-core honors on Windows.

### Terminal backend on Windows

- **Built-in (default).** go-pty's ConPTY backend, which `start_windows.go` already uses. Needed: the Windows default shell (above), agent lookup with `PATHEXT` in `agents.go`, Windows paths for config, and `agent-pane status` writing to `CONOUT$` instead of `/dev/tty`. There is no inherited page descriptor, so `AGENT_PANE_FD` is unset, as `startPty` already does. The host's children are ended with the host; whether Chrome's process handling on Windows ends them sooner (for example through a job object) is a [prototype](#what-needs-a-prototype) item.
- **Durable (preview).** Offered on Windows once the named-pipe client and `lockFile` land, with RFC 0001's behavior. herdr-core 0.1.19's release notes say that on Windows a terminal still ends every process it started when it closes, where Unix lets deliberately detached programs survive; that difference is the core's and is noted in the README.
- **Core missing or failing.** Unchanged from the other platforms: when Durable cannot start a pane tab, it runs in Built-in and the terminal says why (`fallbackNotice` in `serve.go`); when the host cannot tell whether `terminal.run` started it, `tty.create` fails. A missing or wrong `libexec\hc.exe` is a doctor failure that `--fix` repairs. With Built-in the default, a broken core does not stop a Windows user from opening pane tabs.

### Page access

**First Windows release: none.** `agent-pane page` refuses with "page reading is not available on Windows yet", the skill says so for Windows, and `AGENT_PANE_SOCKET` is unset. This is today's `page_windows.go` behavior with a clearer message. Send tab works.

**Later stage: a named-pipe page channel.** The design, for review in that stage's issue, mirrors the unix socket:

- **Endpoint.** For each session the host creates a named pipe `\\.\pipe\agent-pane-<random>` with `FILE_FLAG_FIRST_PIPE_INSTANCE` (so another process cannot have squatted the name) and a DACL that grants only the current user's SID, with the default integrity label so a lower-integrity process cannot connect. Its name goes to the program in `AGENT_PANE_SOCKET`, keeping the variable's meaning: the terminal's own page endpoint. `PIPE_REJECT_REMOTE_CLIENTS` refuses network clients.
- **Peer identity.** On each connection the host reads the client pid with `GetNamedPipeClientProcessId`, opens the process with `PROCESS_QUERY_LIMITED_INFORMATION`, and checks that its token's user SID is the host's (`peercred`'s uid check, in SIDs). `peercred.Peer` gains a SID, or the uid check moves behind a platform function, so `Policy.Allow` no longer compares with `os.Getuid()` on Windows.
- **Ancestry.** Windows records a parent pid when a process is created but does not update it when the parent exits, and pids are reused quickly. A walk over `CreateToolhelp32Snapshot` parent pids must therefore check, at each step, that the parent's creation time (`GetProcessTimes`) is earlier than the child's, and stop at the first parent that has exited. That admits the session's root process and its live descendants, and refuses a process whose recorded parent pid was reused. It refuses a descendant whose intermediate parent has exited, which unix allows today when the orphan is reparented. A program that double-forks and exits its parent, common for daemons, cannot read the page on Windows; review decides whether that is acceptable.
- **Job objects, Built-in only.** In Built-in the host can put the session's root process in a job object of its own and admit a client if `IsProcessInJob` says it is in that job. That is stronger than ancestry: membership is inherited by every descendant, does not depend on parents staying alive, and cannot be faked by pid reuse. A child created with `CREATE_BREAKAWAY_FROM_JOB` leaves the job only if the job allows breakaway, which the host's job would not. In Durable the core's holder creates the process inside herdr-core's own job object, which the host cannot name, so Durable falls back to the ancestry walk from the core-reported root pid, as RFC 0001 does on unix.
- **Threat model.** Same as today's README: Agent Pane is not a sandbox, and any program running as the user can already read the browser profile and drive the core. The page endpoint's job is narrower: page content goes only to the pane tab that asked, and a same-user process outside the pane tab cannot read the page by connecting to its endpoint. Named pipes add two Windows-specific risks: name squatting, answered by `FILE_FLAG_FIRST_PIPE_INSTANCE` and random names, and impersonation in the other direction, where a client connecting to a server it did not expect; the `agent-pane page` client sets `SECURITY_IDENTIFICATION` (not impersonation) and checks with `GetNamedPipeServerProcessId` that the server is an `agent-pane.exe` process of the same user. Processes at other integrity levels, elevated shells in particular, are a case to test: an elevated program in a pane tab would connect from a higher integrity level, which the DACL allows.
- **AF_UNIX on Windows** (supported since Windows 10 1803, and by Go's `net` package) was considered: it would let `page_unix.go` be reused nearly as is, and `SIO_AF_UNIX_GETPEERPID` gives the client pid. But it gives no user identity of its own, file-system ACLs on a socket file in `%LOCALAPPDATA%` are the only access rule, and the core already uses named pipes, so named pipes are proposed. Prototyping may change that.
- **Inherited descriptor.** No `AGENT_PANE_FD` on Windows. Passing a handle through ConPTY's `STARTUPINFOEX` handle list is possible but not in go-pty, and Durable has no descriptor path on any platform.

Shipping Windows first without page access costs Windows users the feature that most sets Agent Pane apart. It is proposed anyway, because the installer, distribution and backend work is large, valuable on its own and low-risk, and the page channel is the one part where a mistake leaks page content. Review may choose instead to hold the first Windows release until page access lands; the rollout order allows either.

### `agent-pane doctor` on Windows

The same checks and messages as today, with Windows sources:

| Check | On Windows |
| --- | --- |
| release | `%LOCALAPPDATA%\agent-pane\release` has the layout above |
| binary | `bin\agent-pane.exe` matches the release copy |
| path | `bin\` is on the user `Path` (new; warning only, since a terminal opened before the install does not see it) |
| native host / manifest | The manifest file has the right `name`, `path` and `allowed_origins`; for each installed browser, its registry key exists and names that file |
| core | Every pinned file matches the lock; `hc.exe --version` matches; the licenses are present |
| core isolation | No `hc.exe` on `Path` is Agent Pane's copy |
| instructions, agent catalog, skill, skill copies, extension, versions | As today, with Windows paths |
| spawn agent | Not checked (macOS only) |

`--fix` runs `agent-pane place` from the release copy and downloads nothing, as today.

### Testing

- **CI, first.** A `go-windows` job on `windows-latest` runs `go vet ./...`, `go test -race ./...` and `go build ./...`; `gofmt` stays on Ubuntu, since its result does not depend on the OS. The first issue makes them pass by giving unix-only tests build tags and fixing shared helpers such as `eventually`; it changes no product behavior. The extension's tests are OS-independent and stay on Ubuntu.
- **Each later stage adds its Windows tests to that job:** the placing step and doctor against a temporary `%LOCALAPPDATA%`, `%APPDATA%` and `HKCU` subkey (tests point the code at a test root so the runner's real browser keys are not touched); the named-pipe core client against a fake core, and with `AGENT_PANE_TEST_HC` against the real pinned `hc.exe`; the page channel's admission and refusal cases, ported from `page_unix_test.go` and `page_socket_unix_test.go`, plus pid-reuse and elevated-process cases.
- **Installer tests.** `scripts/install_test.sh` keeps covering the Unix scripts and the lock parser, including the new `file` lines and a Windows fetch on Linux. A Pester or Go test covers `install.ps1` on the Windows runner against a local archive.
- **Release smoke test.** `release.yml`'s smoke matrix gains `windows-latest`: create a browser user-data folder, run the published `install.ps1`, run it again and check it prints nothing, check `agent-pane --version`, check `agent-pane doctor` passes, and, if the binary is signed, check its Authenticode signature with `Get-AuthenticodeSignature`. Starting a real browser to load the extension is not covered, the same as on the other platforms.
- **Manual, recorded in the PRs:** Chrome and Edge open a pane tab with PowerShell and with Claude Code; resize, paste and Ctrl+C work; an update with the browser open; Durable across a browser quit, once Durable lands on Windows.

### What needs a prototype

1. Chrome's and Edge's launch of the host on Windows: arguments, whether a console window flashes for `agent-pane.exe` (a console-subsystem binary) and whether building it as a GUI-subsystem binary with explicit stdio is needed, and whether Built-in shells survive the host or the browser killing its job.
2. herdr-core finds its ConPTY files when the executable is renamed `hc.exe`, and its named pipe accepts an `HC_SOCKET` override.
3. Durable pane tabs survive a browser quit on Windows: the core is started by the host, so it must not be in a job object that ends with the browser.
4. The registry keys Vivaldi and Helium read on Windows.
5. The page channel's ancestry walk on a real Windows process tree: shells, `cmd /c` chains, npm shims, and programs started through `start`.

## Drawbacks

- **A third platform to test and support**, with its own shell, path, process and console semantics, on a project whose CI and maintainers' machines are macOS and Linux.
- **A second installer language.** `install.ps1` is small, but it is PowerShell, and the placing step on Windows is Go while macOS and Linux keep `place.sh`. Two implementations of one install must be kept in step.
- **A weaker peer check.** Windows has no equivalent of `SO_PEERCRED` plus a kernel-maintained parent pid. The ancestry walk depends on creation-time checks, and Durable cannot use job objects.
- **Windows without page access** is a partial product for a while, and some users will find that out only after installing.
- **Signing costs.** An Authenticode certificate or a signing service is a recurring cost and a release secret; not signing means SmartScreen warnings and blocked installs on managed machines.
- **A new dependency** if go-winio is chosen for named pipes.

## Rationale and alternatives

**Recommended: one product, staged.** The same extension and host protocol, Built-in as the default everywhere, Windows-specific code behind build tags, a Go placing step that shares doctor's description of the install, and page access as its own reviewed stage.

Alternatives considered:

- **Durable only on Windows (#18's acceptance criterion).** Avoids testing the Built-in ConPTY path. But Durable is a preview with readiness checks not yet passed on any platform, and Windows would have no fallback when the core fails. Rejected for now; RFC 0001's stage 6 can make Durable the default on Windows with the others.
- **Point Windows users to WSL.** Agent Pane already runs in WSL2 with a Linux browser, and a Windows browser could reach a WSL host through a wrapper. But the agents and files would be in WSL, not the person's Windows projects, and native-messaging hosts across the WSL boundary are brittle. Worth a README note, not a substitute.
- **A PowerShell port of `place.sh`.** Keeps "the archive's own script places it" as on Unix, but duplicates 447 lines of careful shell in a third language, again repeated in `doctor.go`. Rejected in favor of a Go placing step that doctor can share.
- **Move every platform to the Go placing step.** Would remove the shell duplication everywhere, but changes a tested installer on two platforms for a third. Left as a [future possibility](#future-possibilities).
- **An MSI or MSIX package, winget or Scoop.** Gives Add/Remove Programs and signing integration, but needs a packaging toolchain and, for MSI, usually machine-wide installs. A winget or Scoop manifest pointing at the release zip can come later without changing the layout.
- **A `.bat` host wrapper,** like the sh wrapper. Simple, but it goes through `cmd.exe`, adds a process between the browser and the host, and quoting arguments through `cmd` is error-prone. Rejected for the first-argument rule.
- **AF_UNIX for page access.** See [Page access](#page-access).
- **Page access in the first Windows release.** Possible, and the order in [Rollout](#rollout) allows it by holding issue 9 until issue 8 lands. Not proposed, to keep the trust-boundary review separate.

Cost of doing nothing: Agent Pane stays unavailable to Windows users outside WSL, and #18 stays open.

## Prior art

- **Agent Pane's own unix page socket and `internal/peercred`:** the model for the named-pipe channel; same policy, Windows sources for uid, pid and parent.
- **herdr-core on Windows:** named pipes limited to the current account and SYSTEM, ConPTY with a bundled `OpenConsole.exe`, job objects for cleanup, PowerShell installer `irm ... | iex`, state under `%LOCALAPPDATA%`. Agent Pane follows its installer and transport choices so the two behave alike.
- **Chromium native messaging on Windows:** per-user `HKCU` keys pointing at a manifest file; used by password managers and other extensions with native hosts, which commonly install per user without administrator rights.
- **VS Code and Windows Terminal:** ConPTY with a bundled `OpenConsole.exe` for a newer console than the system's, and PowerShell 7 as the default shell when present.
- **Docker Desktop and the Windows SSH agent:** named pipes with per-user DACLs as the local, same-user IPC boundary.
- **RFC 0001:** the staged rollout and readiness-check approach this RFC copies.

## Unresolved questions

- **Signing.** Should Agent Pane sign `agent-pane.exe` (Authenticode, for example Azure Trusted Signing) before the first Windows release, as it notarizes on macOS, and refuse to publish unsigned Windows binaries from the upstream repository? And is an unsigned bundled `hc.exe` acceptable when herdr-core publishes one?
- **Page access timing.** Ship the first Windows release without page access (proposed), or hold it until the named-pipe channel passes review?
- **Ancestry rules.** Is refusing descendants whose intermediate parent has exited acceptable on Windows, given that unix admits them?
- **Elevated programs.** Should a page read from an elevated process in a pane tab be refused, since the person may not expect an administrator shell to receive page content?
- **Named-pipe client.** go-winio, or a small client on `golang.org/x/sys/windows`?
- **Browser list.** Which registry keys do Vivaldi and Helium read, and does Helium ship for Windows? Brave also reads Chrome's key on some versions; is writing both needed?
- **Config location.** `%APPDATA%\agent-pane` (roaming, proposed, like herdr-core's config) or `%USERPROFILE%\.config\agent-pane` (like the agents' skills folders, and easier to document across platforms)?
- **Binary name of the core.** Keep `hc.exe` (proposed, matching `libexec/hc`), or keep herdr-core's `herdr-core.exe` if the rename breaks its ConPTY lookup?
- **Default shell.** PowerShell 7, then Windows PowerShell (proposed), or `cmd.exe`, or Git Bash when installed, since many coding agents' docs assume a POSIX shell?
- **Placing step on other platforms.** Should macOS and Linux later move from `place.sh` to the same Go placing step?

## Future possibilities

- **Windows on ARM**, when herdr-core ships an aarch64 Windows build.
- **winget and Scoop manifests** that point at the release zip.
- **`agent-pane uninstall`** on every platform.
- **One Go placing step for all platforms,** retiring `place.sh`'s duplication with `doctor.go`.
- **PowerShell completion** in the Windows archive.
- **WSL integration:** a pane tab that runs in a WSL distribution from a Windows install, with its own decision about page access across that boundary.

## Rollout

Proposed implementation issues, in order, each one pull request. None is filed by this RFC.

1. **Windows CI job.** Add `go-windows` (`go vet`, `go test -race`, `go build` on `windows-latest`) to `ci.yml`, and make it pass by tagging unix-only tests and sharing test helpers. No behavior change.
2. **Built-in backend on Windows.** Windows paths in `configDir` and `layoutFor`; PowerShell default shell; `PATHEXT` agent lookup; `agent-pane status` through `CONOUT$`; the `chrome-extension://` first-argument rule; a clear `agent-pane page` refusal and a Windows note in the skill. Tested on the Windows job and manually with a hand-registered manifest.
3. **Windows core in the lock and fetch.** `file` lines in `scripts/herdr-core.lock`, zip handling and the Windows layout in `fetch-herdr-core.sh`, the notices-folder rule, and `install_test.sh` cases. No archive yet.
4. **Windows release archive.** `windows/amd64` in `.goreleaser.yaml` with the Windows file list and the fetched core; `goreleaser check` and a snapshot build in CI.
5. **Windows placing step.** `agent-pane place` for Windows: release copy, binary and core placement with rename-aside, extension, agents, skill copies, instructions, manifest and registry keys, user `Path`; idempotent; tests on the Windows job.
6. **`agent-pane doctor` on Windows.** The checks in [doctor](#agent-pane-doctor-on-windows), `--fix` through the placing step, tests on the Windows job.
7. **`install.ps1` and the Windows release smoke test.** The template, its stamping and publishing in `release.yml`, the `windows-latest` smoke job, and signing if the [signing question](#unresolved-questions) is settled by then.
8. **Durable (preview) on Windows.** Named-pipe transport in `internal/herdrcore`, `LockFileEx` for `lockFile`, core paths, and the prototype items 2 and 3, with the real-core tests on the Windows job.
9. **Document Windows support.** The README's platform line, install, update and uninstall for Windows, the keyboard note, and which features are not on Windows yet. This is the point Windows is announced.
10. **Page access on Windows.** Named-pipe page endpoint, `peercred` for Windows (SID, client pid, creation-time ancestry walk, job objects in Built-in), the `agent-pane page` client's server check, and the ported and new tests. Its PR carries the threat model above for review, and updates the README's Security section.

Issues 1 to 7 and 9 give a Windows release with Built-in pane tabs and no page access; issue 8 can land before or after 9. If review decides page access must be in the first Windows release, issue 9 waits for issue 10.
