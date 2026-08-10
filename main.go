// Command censys_go is an interactive and scriptable client for the Censys API,
// built on the official SDK at https://github.com/censys/censys-sdk-go (MIT).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/manifoldco/promptui"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/render"
	"github.com/mar0ls/censys_go/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "\ninterrupted")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "[Error] %s\n", censysx.Explain(err))
		os.Exit(1)
	}
}

func run() error {
	// A single Ctrl+C cancels in-flight work so partial results still get
	// written; a second one is left to the runtime to abort hard.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "=== Censys-Go CLI %s ===\n", version)

	creds, source, err := config.Resolve("", "")
	if err != nil {
		if !errors.Is(err, config.ErrNotConfigured) {
			return err
		}
		if creds, err = promptForCredentials(); err != nil {
			return err
		}
		source = "prompt"
	}
	fmt.Fprintf(os.Stderr, "[**] credentials from %s (org %s)\n", source, creds.OrgID)

	newClient := func(c config.Credentials) *censysx.Client {
		return censysx.New(censysx.Options{
			OrgID:   c.OrgID,
			Token:   c.Token,
			Timeout: 60 * time.Second,
		})
	}

	return ui.New(ui.Options{
		Client:      newClient(creds),
		Credentials: creds,
		Format:      render.NDJSON,
		NewClient:   newClient,
	}).Run(ctx)
}

// promptForCredentials asks for credentials on first run and offers to store them.
func promptForCredentials() (config.Credentials, error) {
	fmt.Fprintf(os.Stderr, "[Warning] no credentials found; set %s and %s, or enter them now\n",
		config.EnvOrgID, config.EnvToken)

	orgID, err := (&promptui.Prompt{Label: "Organization ID"}).Run()
	if err != nil {
		return config.Credentials{}, err
	}
	token, err := (&promptui.Prompt{Label: "Bearer Token", Mask: '*'}).Run()
	if err != nil {
		return config.Credentials{}, err
	}

	creds := config.Credentials{OrgID: strings.TrimSpace(orgID), Token: strings.TrimSpace(token)}
	if err := creds.Validate(); err != nil {
		return config.Credentials{}, err
	}
	if err := config.Save(creds); err != nil {
		return config.Credentials{}, err
	}

	path, _ := config.Path()
	fmt.Fprintf(os.Stderr, "[OK] credentials saved to %s\n", path)
	return creds, nil
}
