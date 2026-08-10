// Package ui implements the interactive menu.
//
// Prompts, progress and status messages go to the message writer (stderr);
// results go to the output writer (stdout). Keeping them apart is what lets
// `censys > hits.ndjson` produce a clean file while the operator still sees
// what is happening.
package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/manifoldco/promptui"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/render"
)

// Status prefixes, kept for continuity with the previous console output.
const (
	prefixOK   = "[OK]"
	prefixWarn = "[Warning]"
	prefixErr  = "[Error]"
	prefixInfo = "[**]"
)

// UI drives the interactive menu.
type UI struct {
	client *censysx.Client
	creds  config.Credentials
	format render.Format

	in  *bufio.Reader
	out io.Writer
	msg io.Writer

	// newClient rebuilds the client after the operator changes credentials.
	newClient func(config.Credentials) *censysx.Client
}

// Options configures a UI.
type Options struct {
	Client      *censysx.Client
	Credentials config.Credentials
	Format      render.Format

	In  io.Reader
	Out io.Writer
	Msg io.Writer

	NewClient func(config.Credentials) *censysx.Client
}

// New builds a UI, filling in the standard streams when they are not supplied.
func New(opts Options) *UI {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Msg == nil {
		opts.Msg = os.Stderr
	}
	if opts.Format == "" {
		opts.Format = render.NDJSON
	}
	return &UI{
		client:    opts.Client,
		creds:     opts.Credentials,
		format:    opts.Format,
		in:        bufio.NewReader(opts.In),
		out:       opts.Out,
		msg:       opts.Msg,
		newClient: opts.NewClient,
	}
}

// menuItem pairs a menu label with its handler.
type menuItem struct {
	label string
	run   func(context.Context) error
}

// Run shows the menu until the operator exits or ctx is cancelled.
func (u *UI) Run(ctx context.Context) error {
	items := []menuItem{
		{"Show credits and usage", u.credits},
		{"Search hosts", u.search},
		{"View host", u.host},
		{"Bulk view hosts", u.bulk},
		{"Aggregate", u.aggregate},
		{"Certificate lookup", u.certificate},
		{"Output format", u.chooseFormat},
		{"Configure credentials", u.configure},
	}

	labels := make([]string, 0, len(items)+1)
	for _, item := range items {
		labels = append(labels, item.label)
	}
	labels = append(labels, "Exit")

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		prompt := promptui.Select{
			Label:  fmt.Sprintf("Select action (output: %s)", u.format),
			Items:  labels,
			Stdout: nopCloser{u.msg},
			Size:   len(labels),
		}
		idx, _, err := prompt.Run()
		if err != nil {
			// Ctrl+C and Ctrl+D at the menu mean "quit", not "crash".
			if errors.Is(err, promptui.ErrInterrupt) || errors.Is(err, promptui.ErrEOF) || errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("menu: %w", err)
		}
		if idx == len(items) {
			return nil
		}

		if err := items[idx].run(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			u.errorf("%s", censysx.Explain(err))
		}
	}
}

// ── prompts ──────────────────────────────────────────────────────────────────

// ask prints a prompt to the message stream and reads one line from input.
func (u *UI) ask(prompt string) (string, error) {
	u.printf("%s", prompt)
	line, err := u.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// askDefault reads a line, substituting fallback when the operator just hits Enter.
func (u *UI) askDefault(prompt, fallback string) (string, error) {
	answer, err := u.ask(fmt.Sprintf("%s [%s]: ", prompt, fallback))
	if err != nil {
		return "", err
	}
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

// askRequired reads a line and rejects an empty answer.
func (u *UI) askRequired(prompt string) (string, error) {
	answer, err := u.ask(prompt)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return "", errors.New("value cannot be empty")
	}
	return answer, nil
}

// confirm asks a yes/no question. def is the answer for a bare Enter.
func (u *UI) confirm(question string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	answer, err := u.ask(fmt.Sprintf("%s %s: ", question, hint))
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (u *UI) askInt(prompt string, fallback int) (int, error) {
	answer, err := u.askDefault(prompt, strconv.Itoa(fallback))
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(answer)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", answer)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", prompt)
	}
	return value, nil
}

// ── output helpers ───────────────────────────────────────────────────────────

func (u *UI) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(u.msg, format, args...)
}

func (u *UI) infof(format string, args ...any) {
	u.printf(prefixInfo+" "+format+"\n", args...)
}

func (u *UI) okf(format string, args ...any) {
	u.printf(prefixOK+" "+format+"\n", args...)
}

func (u *UI) warnf(format string, args ...any) {
	u.printf(prefixWarn+" "+format+"\n", args...)
}

func (u *UI) errorf(format string, args ...any) {
	u.printf(prefixErr+" "+format+"\n", args...)
}

// stream starts a render stream on the output writer in the selected format.
func (u *UI) stream() *render.Stream {
	return render.NewStream(u.out, u.format)
}

// nopCloser adapts a Writer for promptui, which insists on a WriteCloser.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
