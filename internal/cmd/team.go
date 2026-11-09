package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agent-pane/internal/herdrcore"
)

// Teamwork: a program in a Durable (preview) pane tab starts helpers in the
// bundled core and sends them work, waits for them and reads their output.
// A helper belongs to the pane tab that started it: it carries the pane
// tab's public id, ends when the pane tab's program ends, and is the only
// thing these commands address. It gets no page socket, so page reads stay
// with the pane tab and its approval.

// Labels on a helper's core terminal.
const (
	coreParentPaneLabel = "agent-pane.parent-pane"
	coreHelperLabel     = "agent-pane.helper"
)

// envCoreTerminal is the variable the core sets in each of its terminals to
// that terminal's address.
const envCoreTerminal = "HC_TERMINAL_ID"

// teamWaitTimeout is how long agent-pane wait waits by default: under the
// two minutes coding agents' command tools commonly allow.
const teamWaitTimeout = 90 * time.Second

var errNotDurable = errors.New("helpers need a pane tab on the Durable (preview) terminal backend; choose it in Options under Terminal backend and open a new pane tab")

var helperNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

// teamCore is the part of a core connection the teamwork commands use.
type teamCore interface {
	Get(ctx context.Context, terminal string) (herdrcore.Terminal, error)
	List(ctx context.Context, labels map[string]string, all bool) ([]herdrcore.Terminal, error)
	Run(ctx context.Context, p herdrcore.RunParams) (herdrcore.Terminal, error)
	Remove(ctx context.Context, terminal string) error
	Send(ctx context.Context, p herdrcore.SendParams) (herdrcore.SendResult, error)
	Wait(ctx context.Context, p herdrcore.WaitParams) (herdrcore.WaitResult, error)
	Read(ctx context.Context, p herdrcore.ReadParams) (herdrcore.ReadResult, error)
	Close() error
}

// dialTeamCore connects to Agent Pane's own core, never one HC_* names. It
// does not start the core: with no core running there is no Durable pane
// tab to call from.
var dialTeamCore = func(ctx context.Context) (teamCore, error) {
	c, err := herdrcore.Private(dataDir).Dial(ctx)
	if err != nil {
		return nil, errNotDurable
	}
	return c, nil
}

// lockTeam takes the lock helper launches hold while they choose a name
// and start the helper, and returns its release.
var lockTeam = func(ctx context.Context) (func(), error) {
	return lockFile(ctx, filepath.Join(filepath.Dir(herdrcore.Private(dataDir).Socket), "team.lock"))
}

// team is one command's view: the calling pane tab's core terminal and the
// connection to the core.
type team struct {
	core teamCore
	// caller is the pane tab's core terminal address; pane its public id.
	caller string
	pane   string
}

// openTeam checks that the command runs in a Durable pane tab and connects
// to the core. The core terminal HC_TERMINAL_ID names must carry this pane
// tab's id, so a terminal outside Agent Pane's pane tabs, or a helper,
// cannot act as one.
func openTeam(ctx context.Context, getenv func(string) string) (*team, error) {
	if getenv(envPresence) != "1" {
		return nil, errors.New(errNotInTerminal)
	}
	pane, caller := getenv(envTabID), getenv(envCoreTerminal)
	if pane == "" || caller == "" {
		return nil, errNotDurable
	}
	core, err := dialTeamCore(ctx)
	if err != nil {
		return nil, err
	}
	term, err := core.Get(ctx, caller)
	if err != nil || term.State != "live" || term.Labels[corePaneLabel] != pane {
		core.Close()
		return nil, errNotDurable
	}
	return &team{core: core, caller: caller, pane: pane}, nil
}

// helpers returns this pane tab's helpers, finished ones too, oldest first.
func (t *team) helpers(ctx context.Context) ([]herdrcore.Terminal, error) {
	terms, err := t.core.List(ctx, map[string]string{coreParentPaneLabel: t.pane}, true)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(terms, func(i, j int) bool { return terms[i].CreatedMs < terms[j].CreatedMs })
	return terms, nil
}

// helper finds this pane tab's helper called name: the live one, else the
// newest that finished.
func (t *team) helper(ctx context.Context, name string) (herdrcore.Terminal, error) {
	terms, err := t.helpers(ctx)
	if err != nil {
		return herdrcore.Terminal{}, err
	}
	var found *herdrcore.Terminal
	for i := range terms {
		if terms[i].Labels[coreHelperLabel] != name {
			continue
		}
		if found == nil || found.State != "live" {
			found = &terms[i]
		}
	}
	if found == nil {
		return herdrcore.Terminal{}, fmt.Errorf("no helper named %q in this pane tab; see agent-pane ls", name)
	}
	return *found, nil
}

// helperState is a helper's state in one word or phrase: what it reports,
// "running" when it reports nothing, or how it ended.
func helperState(t herdrcore.Terminal) string {
	if t.State != "live" {
		switch {
		case t.ExitCode != nil:
			return fmt.Sprintf("exited %d", *t.ExitCode)
		case t.Signal != nil:
			return fmt.Sprintf("killed by signal %d", *t.Signal)
		}
		return "ended"
	}
	if t.Status == nil || t.Status.State == "" {
		return "running"
	}
	s := t.Status.State
	if t.Status.Kind != "" {
		s += " (" + t.Status.Kind + ")"
	}
	if t.Status.Msg != "" {
		s += ": " + t.Status.Msg
	}
	return s
}

// helperArgv is what a helper runs: an agent definition with its arguments
// and the rest of args, or args as a command.
func helperArgv(args []string, lookup func(string) (Agent, bool)) (argv []string, agent string) {
	if a, ok := lookup(args[0]); ok {
		argv = append([]string{a.Bin}, a.Args...)
		return append(argv, args[1:]...), a.ID
	}
	return args, ""
}

// freeName is base, or base-2, base-3... when a live helper has it.
func freeName(base string, terms []herdrcore.Terminal) string {
	used := map[string]bool{}
	for _, t := range terms {
		if t.State == "live" {
			used[t.Labels[coreHelperLabel]] = true
		}
	}
	name := base
	for n := 2; used[name]; n++ {
		suffix := "-" + strconv.Itoa(n)
		name = base[:min(len(base), 32-len(suffix))] + suffix
	}
	return name
}

// defaultHelperName makes a helper name from what it runs.
func defaultHelperName(agent string, argv []string) string {
	base := agent
	if base == "" {
		base = argv[0]
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
	}
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, base)
	base = strings.TrimLeft(base, "-_.")
	if base == "" {
		base = "helper"
	}
	return base[:min(len(base), 32)]
}

// runHelper starts a helper as a child of the pane tab's terminal and
// returns its name. It holds the team lock from choosing the name until the
// helper runs, so two launches cannot give live helpers the same name.
func (t *team) runHelper(ctx context.Context, p helperRun) (string, error) {
	if p.name != "" && !helperNamePattern.MatchString(p.name) {
		return "", fmt.Errorf("invalid helper name %q: use 1 to 32 of a-z, 0-9, -, _ and .", p.name)
	}
	unlock, err := lockTeam(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	terms, err := t.helpers(ctx)
	if err != nil {
		return "", err
	}
	name := p.name
	if name == "" {
		name = freeName(defaultHelperName(p.agent, p.argv), terms)
	}
	var finished []string
	for _, term := range terms {
		if term.Labels[coreHelperLabel] != name {
			continue
		}
		if term.State == "live" {
			return "", fmt.Errorf("a helper named %q is already running in this pane tab", name)
		}
		finished = append(finished, term.Address)
	}
	labels := map[string]string{coreParentPaneLabel: t.pane, coreHelperLabel: name}
	if p.agent != "" {
		labels[coreAgentLabel] = p.agent
	}
	_, err = t.core.Run(ctx, herdrcore.RunParams{
		Argv:     p.argv,
		Cwd:      p.cwd,
		Env:      helperEnv(p.env, t.pane, p.agent),
		Labels:   labels,
		Parent:   t.caller,
		NoNotify: true,
	})
	if err != nil {
		return "", err
	}
	// The new helper has the name; the finished ones go only now, so a
	// failed start keeps their results. One left behind is harmless: the
	// name finds the live helper first, then the newest that finished.
	for _, addr := range finished {
		t.core.Remove(ctx, addr)
	}
	return name, nil
}

type helperRun struct {
	name  string
	argv  []string
	agent string
	cwd   string
	env   []string
}

// helperEnv is the caller's environment for a helper: the pane tab's id
// and the agent it runs, but no session id and no page connection.
func helperEnv(base []string, pane, agent string) map[string]string {
	reserved := map[string]string{
		envPresence:    "1",
		envTermProgram: "agent-pane",
		envTabID:       pane,
	}
	if agent != "" {
		reserved[envAgent] = agent
	}
	if info, err := os.Stat(skillPath); err == nil && info.Mode().IsRegular() {
		reserved[envSkillPath] = skillPath
	}
	return coreEnv(withSessionEnv(base, reserved))
}

// waitHelper waits until the helper stops working, or timeout, and says
// how it stands.
func (t *team) waitHelper(ctx context.Context, name string, timeout time.Duration) (string, error) {
	term, err := t.helper(ctx, name)
	if err != nil {
		return "", err
	}
	if term.State == "live" {
		res, err := t.core.Wait(ctx, herdrcore.WaitParams{Terminal: term.Address, Caller: t.caller, Timeout: timeout.Seconds()})
		if err != nil {
			return "", err
		}
		if !res.Ended {
			return name + ": still working", nil
		}
		if term, err = t.core.Get(ctx, term.Address); err != nil {
			return "", err
		}
	}
	return name + ": " + helperState(term), nil
}

func (t *team) sendHelper(ctx context.Context, name, text string, force bool) error {
	term, err := t.helper(ctx, name)
	if err != nil {
		return err
	}
	if term.State != "live" {
		return fmt.Errorf("helper %q has %s", name, helperState(term))
	}
	_, err = t.core.Send(ctx, herdrcore.SendParams{Terminal: term.Address, Text: text, Caller: t.caller, Force: force})
	if herdrcore.IsKind(err, "blocked") {
		return fmt.Errorf("helper %q is waiting for a person; read it with agent-pane read %s, or send with --force to answer it", name, name)
	}
	return err
}

func (t *team) readHelper(ctx context.Context, name string, lines int) (string, error) {
	term, err := t.helper(ctx, name)
	if err != nil {
		return "", err
	}
	res, err := t.core.Read(ctx, herdrcore.ReadParams{Terminal: term.Address, Lines: lines, Caller: t.caller})
	return res.Text, err
}

func (t *team) listHelpers(ctx context.Context, w io.Writer) error {
	terms, err := t.helpers(ctx)
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		_, err := fmt.Fprintln(w, "no helpers in this pane tab; start one with agent-pane run")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HELPER\tSTATE\tCOMMAND")
	for _, term := range terms {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", term.Labels[coreHelperLabel], helperState(term), strings.Join(term.Argv, " "))
	}
	return tw.Flush()
}

// withTeam runs f with the calling pane tab's team, under a deadline of
// coreCallTimeout plus extra.
func withTeam(extra time.Duration, f func(ctx context.Context, t *team) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout+extra)
	defer cancel()
	t, err := openTeam(ctx, os.Getenv)
	if err != nil {
		return err
	}
	defer t.core.Close()
	return f(ctx, t)
}

const teamHelp = `

Helpers run in the terminal runtime bundled with Agent Pane, as part of the
pane tab that started them: they end when its program ends, and only that
pane tab can address them. Helpers cannot read the page. These commands work
in pane tabs on the Durable (preview) terminal backend.`

func NewCmdLs() *cobra.Command {
	return &cobra.Command{
		Use:          "ls",
		Short:        "List this pane tab's helpers and what they are doing",
		Long:         "List the helpers this pane tab started, with their state: what each reports\n(working, blocked, done, error, idle), running when it reports nothing, or\nhow it ended." + teamHelp,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTeam(0, func(ctx context.Context, t *team) error {
				return t.listHelpers(ctx, cmd.OutOrStdout())
			})
		},
	}
}

func NewCmdRun() *cobra.Command {
	var name, cwd string
	cmd := &cobra.Command{
		Use:   "run [--name <name>] <agent|command> [args...]",
		Short: "Start a helper agent or command beside this pane tab",
		Long: `Start a helper and print its name. The first argument is an agent from
the launcher menu (claude, codex, ...), started with its own arguments and
then the rest, or else a command.

  agent-pane run --name review codex "review the diff in src/"
  agent-pane run --name tests -- go test ./...

The helper starts in this directory unless --cwd says otherwise. Without
--name it is named after the agent or command.` + teamHelp,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv, agent := helperArgv(args, agentByID)
			if cwd == "" {
				var err error
				if cwd, err = os.Getwd(); err != nil {
					return err
				}
			}
			return withTeam(coreStartTimeout, func(ctx context.Context, t *team) error {
				got, err := t.runHelper(ctx, helperRun{name: name, argv: argv, agent: agent, cwd: cwd, env: os.Environ()})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), got)
				return err
			})
		},
	}
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&name, "name", "", "the helper's name: 1 to 32 of a-z, 0-9, -, _ and .")
	cmd.Flags().StringVar(&cwd, "cwd", "", "the directory it starts in (default: this one)")
	return cmd
}

func NewCmdSend() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "send [--force] <helper> <message...>",
		Short: "Type a message into a helper and press Enter",
		Long: `Type a message into a helper and press Enter.

  agent-pane send review "also check the tests"

A helper that is blocked is waiting for a person (a permission prompt, a
question): send refuses it, so a message does not answer a prompt meant for
the person. Read it first; --force types anyway.` + teamHelp,
		Args:         cobra.MinimumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withTeam(0, func(ctx context.Context, t *team) error {
				return t.sendHelper(ctx, args[0], strings.Join(args[1:], " "), force)
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "type even when the helper is blocked")
	return cmd
}

func NewCmdWait() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait [--timeout <duration>] <helper>",
		Short: "Wait until a helper stops working",
		Long: `Wait until a helper stops working: it reports done, blocked, error or
idle, or it exits. Then print how it stands.

  agent-pane wait review
  review: done: no issues found

After agent-pane send, it waits for the turn that message started. A helper
that never reports its status counts as working until it exits. After
--timeout (default 90s) it prints "<helper>: still working"; run it again to
keep waiting. Use it instead of sleeping.` + teamHelp,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return errors.New("--timeout must be positive")
			}
			return withTeam(timeout, func(ctx context.Context, t *team) error {
				out, err := t.waitHelper(ctx, args[0], timeout)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), out)
				return err
			})
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", teamWaitTimeout, "how long to wait before reporting it is still working")
	return cmd
}

func NewCmdRead() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "read [--lines <n>] <helper>",
		Short: "Print a helper's screen and recent output",
		Long: `Print a helper's screen as text, with --lines lines of scrollback above
it. It works after the helper has exited too.

  agent-pane read --lines 60 review` + teamHelp,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines < 0 {
				return errors.New("--lines must not be negative")
			}
			return withTeam(0, func(ctx context.Context, t *team) error {
				text, err := t.readHelper(ctx, args[0], lines)
				if err != nil {
					return err
				}
				if text != "" && !strings.HasSuffix(text, "\n") {
					text += "\n"
				}
				_, err = io.WriteString(cmd.OutOrStdout(), text)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&lines, "lines", 0, "lines of scrollback above the screen")
	return cmd
}
