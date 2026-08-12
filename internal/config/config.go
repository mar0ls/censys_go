// Package config resolves and persists Censys API credentials.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir is the credential directory, relative to the user's home directory.
const Dir = ".censys"

// FileName is the credential file inside Dir.
const FileName = "config.json"

// Environment variables consulted by FromEnv.
const (
	EnvOrgID = "CENSYS_ORG"
	EnvToken = "CENSYS_TOKEN" // #nosec G101 -- variable name, not a credential
)

// Credentials identify an organization and the token used to act on its behalf.
type Credentials struct {
	OrgID string `json:"org_id"`
	Token string `json:"token"`
}

// Source records where a set of credentials came from, so the CLI can report it
// without echoing the token itself.
type Source string

const (
	SourceFlags Source = "flags"
	SourceEnv   Source = "environment"
	SourceFile  Source = "config file"
	SourceNone  Source = "none"
)

// ErrNotConfigured is returned by Resolve when no source supplied credentials.
var ErrNotConfigured = errors.New("no credentials found in flags, environment, or config file")

// Validate reports whether both fields are populated.
func (c Credentials) Validate() error {
	if c.OrgID == "" {
		return errors.New("organization ID cannot be empty")
	}
	if c.Token == "" {
		return errors.New("bearer token cannot be empty")
	}
	return nil
}

// complete reports whether both fields are set, without allocating an error.
func (c Credentials) complete() bool {
	return c.OrgID != "" && c.Token != ""
}

// Redacted returns a copy safe to print or serialize in diagnostics.
func (c Credentials) Redacted() Credentials {
	if c.Token != "" {
		c.Token = "***"
	}
	return c
}

// Path returns the absolute path of the credential file.
func Path() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, Dir, FileName), nil
}

// Load reads credentials from disk. A missing file is reported as os.ErrNotExist,
// so callers can distinguish "not configured yet" from a genuine read failure.
func Load() (Credentials, error) {
	path, err := Path()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the user's own home directory
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return c, nil
}

// Save writes credentials to disk with owner-only permissions.
func Save(c Credentials) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// FromEnv reads credentials from the environment. Unset variables yield empty
// fields rather than an error, so the result can be layered under other sources.
func FromEnv() Credentials {
	return Credentials{
		OrgID: strings.TrimSpace(os.Getenv(EnvOrgID)),
		Token: strings.TrimSpace(os.Getenv(EnvToken)),
	}
}

// Resolve picks credentials from the first source that supplies both fields:
// explicit flags, then the environment, then the config file.
//
// Nothing is written to disk here. Credentials supplied through the environment
// stay in the process; persisting them would leak the token onto the filesystem
// of every CI runner and container that sets CENSYS_TOKEN.
func Resolve(flagOrgID, flagToken string) (Credentials, Source, error) {
	fromFlags := Credentials{
		OrgID: strings.TrimSpace(flagOrgID),
		Token: strings.TrimSpace(flagToken),
	}
	if fromFlags.complete() {
		return fromFlags, SourceFlags, nil
	}

	if env := FromEnv(); env.complete() {
		return env, SourceEnv, nil
	}

	stored, err := Load()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, SourceNone, ErrNotConfigured
		}
		return Credentials{}, SourceNone, err
	}
	if err := stored.Validate(); err != nil {
		return Credentials{}, SourceNone, fmt.Errorf("stored credentials are unusable: %w", err)
	}
	return stored, SourceFile, nil
}

// homeDir resolves the user's home directory, falling back to the conventional
// environment variables for setups where os.UserHomeDir fails (some CI images).
func homeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home, nil
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if home := os.Getenv(key); home != "" {
			return home, nil
		}
	}
	return "", errors.New("cannot determine the user's home directory")
}
