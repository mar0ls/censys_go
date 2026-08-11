// Package cli implements the command-line surface: global flags, subcommand
// dispatch, and the wiring between them and the internal packages.
//
// Results go to Env.Out (stdout) and everything else to Env.Msg (stderr), so
// `censys search -q ... > hits.ndjson` yields a file containing only results.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/render"
)

// ErrUsage signals that the command line itself was wrong, so the caller can
// exit 2 rather than 1.
var ErrUsage = errors.New("usage")

// Interrupted reports whether err is the consequence of ctx being cancelled
// rather than a genuine failure.
//
// Testing the error is not enough. signal.NotifyContext attaches a cause, and
// net/http reports that cause instead of context.Canceled, so
// errors.Is(err, context.Canceled) is false for anything that failed in
// transit. The context itself is the reliable signal.
func Interrupted(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil
}

// Env carries the process environment a command runs against.
type Env struct {
	In      io.Reader
	Out     io.Writer
	Msg     io.Writer
	Version string
}

// globals holds the flags every subcommand shares.
type globals struct {
	orgID   string
	token   string
	format  string
	output  string
	timeout time.Duration
	noRetry bool
	quiet   bool
	apiURL  string
}

func (g *globals) register(fs *flag.FlagSet) {
	fs.StringVar(&g.orgID, "org", "", "Censys organization ID (env "+config.EnvOrgID+")")
	fs.StringVar(&g.token, "token", "", "Censys API token (env "+config.EnvToken+")")
	fs.StringVar(&g.format, "format", string(render.NDJSON), "output format: "+render.FormatNames())
	fs.StringVar(&g.output, "output", "-", "write results to a file, or - for stdout")
	fs.DurationVar(&g.timeout, "timeout", 60*time.Second, "budget for one API call, retries included")
	fs.BoolVar(&g.noRetry, "no-retry", false, "attempt each API call once")
	fs.BoolVar(&g.quiet, "quiet", false, "suppress progress and status messages")
	fs.StringVar(&g.apiURL, "api-url", "", "override the API endpoint, for a proxy or a capture")
}

// command is one subcommand.
type command struct {
	name    string
	args    string
	summary string
	// register adds the command's own flags; it may be nil.
	register func(*flag.FlagSet)
	run      func(context.Context, *session, []string) error
}

// Run parses args and dispatches to a subcommand. With no arguments it starts
// the interactive menu.
func Run(ctx context.Context, env Env, args []string) error {
	env = withDefaults(env)
	cmds := commands()

	// Locate the command with a throwaway flag set, so nothing is bound to the
	// globals that the real parse below will populate.
	probeFS := flag.NewFlagSet("censys", flag.ContinueOnError)
	probeFS.SetOutput(io.Discard)
	(&globals{}).register(probeFS)

	name, rest := splitCommand(probeFS, args)
	if name == "" {
		name = "ui"
	}

	var g globals
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Msg)
	g.register(fs)

	switch name {
	case "help":
		fs.Usage = func() { printUsage(env, fs, cmds) }
		printUsage(env, fs, cmds)
		return nil
	case "version":
		_, _ = fmt.Fprintln(env.Out, env.Version)
		return nil
	}

	cmd := lookup(cmds, name)
	if cmd == nil {
		fs.Usage = func() { printUsage(env, fs, cmds) }
		printUsage(env, fs, cmds)
		return fmt.Errorf("%w: unknown command %q", ErrUsage, name)
	}

	fs.Usage = func() { printCommandUsage(env, fs, cmd) }
	if cmd.register != nil {
		cmd.register(fs)
	}

	// Global and command flags are parsed together, on either side of the
	// command name, so `censys host 1.2.3.4 --format table` works.
	if err := fs.Parse(permute(fs, rest)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}

	sess, cleanup, err := newSession(env, &g)
	if err != nil {
		return err
	}
	defer cleanup()

	return cmd.run(ctx, sess, fs.Args())
}

func withDefaults(env Env) Env {
	if env.In == nil {
		env.In = os.Stdin
	}
	if env.Out == nil {
		env.Out = os.Stdout
	}
	if env.Msg == nil {
		env.Msg = os.Stderr
	}
	if env.Version == "" {
		env.Version = "dev"
	}
	return env
}

func lookup(cmds []command, name string) *command {
	for i := range cmds {
		if cmds[i].name == name {
			return &cmds[i]
		}
	}
	return nil
}

func printUsage(env Env, fs *flag.FlagSet, cmds []command) {
	w := env.Msg
	_, _ = fmt.Fprintf(w, "censys %s - search and pivot on Censys data\n\n", env.Version)
	_, _ = fmt.Fprintf(w, "Usage:\n  censys [flags] <command> [flags] [arguments]\n\n")
	_, _ = fmt.Fprintf(w, "Running censys with no command starts the interactive menu.\n\nCommands:\n")

	sorted := append([]command(nil), cmds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range sorted {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	_, _ = fmt.Fprintf(tw, "  %s\t%s\n", "version", "print the version and exit")
	_, _ = fmt.Fprintf(tw, "  %s\t%s\n", "help", "print this message")
	_ = tw.Flush()

	_, _ = fmt.Fprintf(w, "\nFlags:\n")
	fs.PrintDefaults()

	_, _ = fmt.Fprintf(w, "\nExamples:\n%s\n", strings.Join(examples, "\n"))
}

func printCommandUsage(env Env, fs *flag.FlagSet, cmd *command) {
	_, _ = fmt.Fprintf(env.Msg, "%s\n\nUsage:\n  censys %s %s\n\nFlags:\n", cmd.summary, cmd.name, cmd.args)
	fs.PrintDefaults()
}

var examples = []string{
	`  censys search -q 'services.port:9001 and services.tls.certificates.leaf_data.subject.organization:"cobaltstrike"' --pages 0 > c2.ndjson`,
	`  censys host 198.51.100.7 --format table`,
	`  censys aggregate -q 'services.jarm.fingerprint:"07d14d16d21d21d"' --field location.country`,
	`  cat suspects.txt | censys host --format ndjson | jq -r 'select(.asn == 64500) | .ip'`,
}

// session bundles everything a command needs at run time.
type session struct {
	env    Env
	client *censysx.Client
	format render.Format
	out    io.Writer
	msg    io.Writer
}

// newSession resolves credentials, builds the client, and opens the output
// sink. The returned cleanup closes the sink if it is a file.
func newSession(env Env, g *globals) (*session, func(), error) {
	format, err := render.ParseFormat(g.format)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUsage, err)
	}

	creds, source, err := config.Resolve(g.orgID, g.token)
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			return nil, nil, fmt.Errorf("no credentials: pass --org/--token, set %s and %s, or run censys with no arguments to configure interactively",
				config.EnvOrgID, config.EnvToken)
		}
		return nil, nil, err
	}

	msg := env.Msg
	if g.quiet {
		msg = io.Discard
	}

	out := env.Out
	cleanup := func() {}
	if g.output != "" && g.output != "-" {
		f, err := os.Create(g.output) // #nosec G304 -- the path is supplied by the operator running the tool
		if err != nil {
			return nil, nil, fmt.Errorf("opening %s: %w", g.output, err)
		}
		out = f
		cleanup = func() { _ = f.Close() }
	}

	_, _ = fmt.Fprintf(msg, "[**] org %s (credentials from %s)\n", creds.OrgID, source)

	return &session{
		env: env,
		client: censysx.New(censysx.Options{
			OrgID:        creds.OrgID,
			Token:        creds.Token,
			Timeout:      g.timeout,
			DisableRetry: g.noRetry,
			BaseURL:      g.apiURL,
		}),
		format: format,
		out:    out,
		msg:    msg,
	}, cleanup, nil
}

func (s *session) stream() *render.Stream { return render.NewStream(s.out, s.format) }

func (s *session) infof(format string, args ...any) {
	_, _ = fmt.Fprintf(s.msg, "[**] "+format+"\n", args...)
}

func (s *session) warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.msg, "[Warning] "+format+"\n", args...)
}

func (s *session) okf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.msg, "[OK] "+format+"\n", args...)
}
