package profile

// Credentials are the other half of provisioning, and the half a person cannot see: an arm is a
// brand-new profile, so it can sign in only with what evalctl copies into it.
//
// Two kinds of sign-in exist. A plain API key lives in the profile's `.env` and has always been
// copied. A browser login leaves no variable behind — Hermes writes the session to a file, either in
// the profile directory (`google_token.json`, `*_credentials.json`) or once at the Hermes root
// (`auth.json`, which holds the provider credential pool) — and a sandbox that does not get those
// files cannot authenticate. The result used to look like "the agent failed", which reads as a bad
// skill rather than a missing credential; now the copy happens here and the absence is reported
// before a run spends anything.
//
// Nothing in this file prints a value: the run line names the variables and the files, never what is
// inside them.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AuthFiles are the login files a browser sign-in leaves behind. `token.json` and `auth.json` are
// Hermes's own; the suffixes below catch the provider-specific spellings (`google_token.json`,
// `notion_credentials.json`) without an ever-growing list.
var AuthFiles = []string{"auth.json", "credentials.json", "oauth.json", "token.json"}

var authSuffixes = []string{"_credentials.json", "_token.json", "_client_secret.json", "_oauth.json"}

// authName reports whether a file name is a credential.
func authName(name string) bool {
	for _, known := range AuthFiles {
		if name == known {
			return true
		}
	}
	for _, suffix := range authSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// authSource is one credential file to copy: where it lives now and the name it takes in a sandbox.
type authSource struct {
	Name string // the file name, identical in the sandbox
	Path string // where it is read from
	From string // "profile" or "hermes" — where it came from
}

// authSources lists every credential file an arm may need, profile first. A file the profile itself
// carries shadows the root copy of the same name.
func (m *Manager) authSources() []authSource {
	var out []authSource
	seen := map[string]bool{}
	add := func(name, path, from string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, authSource{Name: name, Path: path, From: from})
	}
	// The Hermes root carries the provider credential pool once, for every profile; when the real
	// profile *is* that root the first loop already took it, so the second is skipped.
	roots := []struct{ dir, from string }{{m.Home(m.Real), "profile"}}
	if filepath.Clean(m.Home(m.Real)) != filepath.Clean(m.Hermes) {
		roots = append(roots, struct{ dir, from string }{m.Hermes, "hermes"})
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && authName(e.Name()) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			add(name, filepath.Join(root.dir, name), root.from)
		}
	}
	return out
}

// Credentials is what the real profile can sign an arm in with.
type Credentials struct {
	Profile   string   `json:"profile"`
	EnvKeys   []string `json:"env_keys"`
	AuthFiles []string `json:"auth_files"`
}

// OK reports whether an arm has any way in.
func (c Credentials) OK() bool { return len(c.EnvKeys) > 0 || len(c.AuthFiles) > 0 }

// Describe is the one line a run prints before it spends: which credentials the arms inherit, and
// from where. It names variables and files, never values.
func (c Credentials) Describe() string {
	var parts []string
	if len(c.EnvKeys) > 0 {
		// A profile with a dozen keys is normal; the line's job is "yes, there are keys", not the
		// inventory. `evalctl profiles status` prints the list.
		shown := c.EnvKeys
		extra := ""
		if len(shown) > 4 {
			extra = fmt.Sprintf(" +%d more", len(shown)-4)
			shown = shown[:4]
		}
		parts = append(parts, "env "+strings.Join(shown, ", ")+extra)
	}
	if len(c.AuthFiles) > 0 {
		parts = append(parts, "login "+strings.Join(c.AuthFiles, ", "))
	}
	if len(parts) == 0 {
		return "no credentials"
	}
	return strings.Join(parts, " · ")
}

// Credentials inspects the real profile: the keys its .env holds and the login files it (or the
// Hermes root beside it) carries.
func (m *Manager) Credentials() Credentials {
	c := Credentials{Profile: m.Real, EnvKeys: m.EnvKeysIn(m.Real), AuthFiles: []string{}}
	for _, src := range m.authSources() {
		c.AuthFiles = append(c.AuthFiles, src.Name)
	}
	return c
}

// CredentialProblem is what a run checks before it spends: a real profile with neither an API key
// nor a login file cannot produce a measurable arm, and saying so now is cheaper than an iteration
// of zeros.
func (m *Manager) CredentialProblem() error {
	if m.Credentials().OK() {
		return nil
	}
	return fmt.Errorf("profile %s has no credentials to hand an eval sandbox: %s holds no API key and "+
		"neither the profile nor %s carries a login file (%s). Sign in once in that profile, or put a "+
		"key in its .env — `evalctl profiles status` shows what it has", m.Real,
		filepath.Join(m.Home(m.Real), ".env"), m.Hermes, strings.Join(AuthFiles, ", "))
}

// CopyAuth installs the real profile's login files into a sandbox, byte for byte. An arm runs the
// same account as the profile it measures; only the skill it installs differs.
func (m *Manager) CopyAuth(name string, dry bool) (string, error) {
	if name == m.Real {
		return "auth " + name + ": the real profile's own login, left alone", nil
	}
	var copied []string
	for _, src := range m.authSources() {
		dst := filepath.Join(m.Home(name), src.Name)
		if b, err := os.ReadFile(dst); err == nil {
			if b2, err2 := os.ReadFile(src.Path); err2 == nil && string(b) == string(b2) {
				copied = append(copied, src.Name)
				continue
			}
		}
		if !dry {
			if err := copyFile(src.Path, dst); err != nil {
				return "", fmt.Errorf("auth %s: %s: %w", name, src.Path, err)
			}
			_ = os.Chmod(dst, 0o600)
		}
		copied = append(copied, src.Name)
	}
	if len(copied) == 0 {
		return "auth " + name + ": no login file in " + m.Real + " — .env keys only", nil
	}
	return "auth " + name + " ← " + strings.Join(copied, ", "), nil
}
