package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/url"
	"reflect"
	"testing"
)

func globalsFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("censys", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	(&globals{}).register(fs)
	return fs
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantName string
		wantRest []string
	}{
		{
			"command first",
			[]string{"search", "-q", "x"},
			"search", []string{"-q", "x"},
		},
		{
			"global flag before command",
			[]string{"--format", "table", "host", "1.2.3.4"},
			"host", []string{"--format", "table", "1.2.3.4"},
		},
		{
			// "table" is the value of --format, not the command.
			"flag value must not be mistaken for the command",
			[]string{"--format", "csv", "search"},
			"search", []string{"--format", "csv"},
		},
		{
			"boolean flag consumes nothing",
			[]string{"--quiet", "credits"},
			"credits", []string{"--quiet"},
		},
		{
			"inline flag value",
			[]string{"--format=json", "host"},
			"host", []string{"--format=json"},
		},
		{
			"single dash form",
			[]string{"-quiet", "host", "1.2.3.4"},
			"host", []string{"-quiet", "1.2.3.4"},
		},
		{
			"no command",
			[]string{"--quiet"},
			"", []string{"--quiet"},
		},
		{
			"empty",
			nil,
			"", nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, rest := splitCommand(globalsFlagSet(), tc.args)
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestPermuteMovesFlagsAhead(t *testing.T) {
	fs := globalsFlagSet()
	var value string
	fs.StringVar(&value, "q", "", "query")

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			"flags after positionals",
			[]string{"1.2.3.4", "--format", "csv"},
			[]string{"--format", "csv", "--", "1.2.3.4"},
		},
		{
			"interleaved",
			[]string{"--quiet", "1.2.3.4", "-q", "x", "5.6.7.8"},
			[]string{"--quiet", "-q", "x", "--", "1.2.3.4", "5.6.7.8"},
		},
		{
			"already ordered",
			[]string{"--quiet", "-q", "x"},
			[]string{"--quiet", "-q", "x"},
		},
		{
			"explicit separator keeps the rest positional",
			[]string{"--quiet", "--", "--not-a-flag"},
			[]string{"--quiet", "--", "--not-a-flag"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := permute(fs, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("permute() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The permuted result must still parse to the same values the flags describe.
func TestPermuteRoundTripsThroughParse(t *testing.T) {
	fs := globalsFlagSet()
	var query string
	fs.StringVar(&query, "q", "", "query")

	if err := fs.Parse(permute(fs, []string{"1.2.3.4", "-q", "services.port:443", "--quiet", "5.6.7.8"})); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if query != "services.port:443" {
		t.Errorf("q = %q", query)
	}
	if want := []string{"1.2.3.4", "5.6.7.8"}; !reflect.DeepEqual(fs.Args(), want) {
		t.Errorf("positional args = %v, want %v", fs.Args(), want)
	}
}

// signal.NotifyContext attaches a cause, and net/http reports that cause rather
// than context.Canceled, so an errors.Is check against context.Canceled misses
// every request that failed in transit. Interrupted must not rely on it.
func TestInterruptedDetectsCancellationThroughAWrappedCause(t *testing.T) {
	cause := errors.New("interrupt signal received")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)

	transportErr := &url.Error{Op: "Get", URL: "http://example.test", Err: cause}
	if errors.Is(transportErr, context.Canceled) {
		t.Fatal("premise broken: the transport error now unwraps to context.Canceled")
	}
	if !Interrupted(ctx, transportErr) {
		t.Error("Interrupted() = false for an error raised while the context was cancelled")
	}
}

func TestInterruptedIgnoresOrdinaryFailures(t *testing.T) {
	if Interrupted(context.Background(), errors.New("connection refused")) {
		t.Error("Interrupted() = true for a live context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Interrupted(ctx, nil) {
		t.Error("Interrupted() = true without an error")
	}
}

func TestConsumesNextIgnoresUnknownFlags(t *testing.T) {
	fs := globalsFlagSet()
	if consumesNext(fs, "--nonexistent") {
		t.Error("an unknown flag must not swallow the next token")
	}
}
