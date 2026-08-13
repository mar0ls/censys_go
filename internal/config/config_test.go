package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// isolateHome points the config package at a temporary home directory so tests
// never touch the developer's real ~/.censys.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestResolvePrefersFlagsOverEnvAndFile(t *testing.T) {
	isolateHome(t)
	t.Setenv(EnvOrgID, "env-org")
	t.Setenv(EnvToken, "env-token")
	if err := Save(Credentials{OrgID: "file-org", Token: "file-token"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, src, err := Resolve("flag-org", "flag-token")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src != SourceFlags {
		t.Errorf("source = %q, want %q", src, SourceFlags)
	}
	if got.OrgID != "flag-org" || got.Token != "flag-token" {
		t.Errorf("credentials = %+v, want flag values", got.Redacted())
	}
}

func TestResolvePrefersEnvOverFile(t *testing.T) {
	isolateHome(t)
	t.Setenv(EnvOrgID, "env-org")
	t.Setenv(EnvToken, "env-token")
	if err := Save(Credentials{OrgID: "file-org", Token: "file-token"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, src, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src != SourceEnv {
		t.Errorf("source = %q, want %q", src, SourceEnv)
	}
	if got.OrgID != "env-org" {
		t.Errorf("OrgID = %q, want env-org", got.OrgID)
	}
}

// --org has to win even when the token comes from elsewhere; resolving both
// fields from a single source would silently discard the flag.
func TestResolveLayersOrgOverTokenSource(t *testing.T) {
	isolateHome(t)
	t.Setenv(EnvOrgID, "")
	t.Setenv(EnvToken, "env-token")

	got, src, err := Resolve("flag-org", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src != SourceEnv {
		t.Errorf("source = %q, want the token's source %q", src, SourceEnv)
	}
	if got.Token != "env-token" {
		t.Errorf("Token = %q, want env-token", got.Token)
	}
	if got.OrgID != "flag-org" {
		t.Errorf("OrgID = %q, want the flag to win", got.OrgID)
	}
}

// The organization is optional: asset lookups work without one.
func TestResolveAcceptsTokenWithoutOrg(t *testing.T) {
	isolateHome(t)
	t.Setenv(EnvOrgID, "")
	t.Setenv(EnvToken, "")

	got, src, err := Resolve("", "flag-token")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src != SourceFlags {
		t.Errorf("source = %q, want %q", src, SourceFlags)
	}
	if got.OrgID != "" {
		t.Errorf("OrgID = %q, want empty", got.OrgID)
	}
}

// A stored organization still applies when the token arrives from the
// environment, which is the shape of a CI run against a saved config.
func TestResolveTakesOrgFromFileWhenTokenFromEnv(t *testing.T) {
	isolateHome(t)
	if err := Save(Credentials{OrgID: "file-org", Token: "file-token"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv(EnvOrgID, "")
	t.Setenv(EnvToken, "env-token")

	got, src, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src != SourceEnv || got.Token != "env-token" {
		t.Errorf("token = %q from %q, want env-token from environment", got.Token, src)
	}
	if got.OrgID != "file-org" {
		t.Errorf("OrgID = %q, want file-org", got.OrgID)
	}
}

// Credentials taken from the environment must stay in the process. Writing them
// out would drop the token onto the filesystem of every CI runner.
func TestResolveFromEnvDoesNotWriteToDisk(t *testing.T) {
	home := isolateHome(t)
	t.Setenv(EnvOrgID, "env-org")
	t.Setenv(EnvToken, "env-token")

	if _, _, err := Resolve("", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, Dir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file exists after env resolution (err = %v); token was persisted", err)
	}
}

func TestResolveWithoutAnySource(t *testing.T) {
	isolateHome(t)
	t.Setenv(EnvOrgID, "")
	t.Setenv(EnvToken, "")

	_, src, err := Resolve("", "")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if src != SourceNone {
		t.Errorf("source = %q, want %q", src, SourceNone)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	home := isolateHome(t)
	if err := Save(Credentials{OrgID: "org", Token: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(home, Dir, FileName))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file mode = %04o, want 0600", perm)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolateHome(t)
	want := Credentials{OrgID: "org-1", Token: "tok-1"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got.Redacted(), want.Redacted())
	}
}

func TestLoadMissingFileReportsNotExist(t *testing.T) {
	isolateHome(t)
	if _, err := Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		creds   Credentials
		wantErr bool
	}{
		{"complete", Credentials{OrgID: "o", Token: "t"}, false},
		{"missing token", Credentials{OrgID: "o"}, true},
		// The API accepts asset lookups with no organization, so the token
		// alone is enough to start.
		{"token only", Credentials{Token: "t"}, false},
		{"empty", Credentials{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.creds.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRedactedHidesToken(t *testing.T) {
	got := Credentials{OrgID: "org", Token: "super-secret"}.Redacted()
	if got.Token == "super-secret" {
		t.Error("Redacted() returned the token verbatim")
	}
	if got.OrgID != "org" {
		t.Errorf("Redacted() changed OrgID to %q", got.OrgID)
	}
}
