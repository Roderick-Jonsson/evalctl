package llm

import (
	"os"
	"path/filepath"
	"strings"
)

// Where the key comes from. Not hard-coded, not in the tree, not written by this tool: the process
// environment first, then the first env file that has it. That is the whole policy, and it is a
// policy rather than a lookup because a key in a repository is a key on GitHub.
//
// The order is the one every CLI converges on — the environment wins, because that is what a shell
// profile, a CI secret and a `FOO=bar evalctl …` prefix all use; the files are the convenience for a
// laptop, and the nearest one wins. A file never overrides a variable that is already set, so a stale
// .env cannot silently beat the key somebody just exported.
//
// Nothing here prints or stores the key: the screens show which VARIABLE was found and in WHICH file,
// which is what a person needs to fix a wrong key, and never the value.

// EnvFileVar points the lookup at one file and nothing else, for a machine whose key lives somewhere
// the defaults do not look.
const EnvFileVar = "EVALCTL_ENV_FILE"

// envFiles is the list of files consulted, in order: the nearest .env walking up from the working
// directory, then the per-user config file, then the harness's own .env (which is where a key that
// already works for `hermes` lives on this machine).
func envFiles() []string {
	if p := strings.TrimSpace(os.Getenv(EnvFileVar)); p != "" {
		return []string{expandHome(p)}
	}
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 6; i++ {
			add(filepath.Join(dir, ".env"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(cfg, "evalctl", "env"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".hermes", ".env"))
	}
	return out
}

// envValue is one variable's value and where it came from: "the environment" or the file's path. A
// variable set to the empty string counts as unset, because an exported empty key is a mistake and
// not an answer.
func envValue(name string) (value, from string) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, "the environment"
	}
	for _, p := range envFiles() {
		if v := readEnvFile(p)[name]; v != "" {
			return v, displayPath(p)
		}
	}
	return "", ""
}

// readEnvFile parses one file, missing or unreadable meaning no variables: a key file that is not
// there is the normal case, not an error to report.
func readEnvFile(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseEnvFile(string(b))
}

// parseEnvFile reads the dotenv dialect a person actually writes: blank lines and #-comments,
// an optional `export `, single or double quotes, a trailing comment after an unquoted value, and
// CRLF from an editor that has never met this filesystem.
func parseEnvFile(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		switch {
		case len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\''):
			val = val[1 : len(val)-1]
		case strings.HasPrefix(val, `"`):
			// An unterminated double quote is a paste that lost its end: take what is there.
			val = strings.TrimPrefix(val, `"`)
		default:
			// An inline comment is only a comment when whitespace separates it: a # inside a value
			// (a URL fragment, a password) is part of the value.
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		if val != "" {
			out[key] = val
		}
	}
	return out
}

// displayPath shortens the home directory to ~, because a status row has one line and the part that
// matters is which file, not the username.
func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// searchedEnvFiles is the list an error message names: the files that were actually consulted, so a
// person who put the key in the wrong one can see that it was not read.
func searchedEnvFiles() []string {
	var out []string
	for _, p := range envFiles() {
		if isFile(p) {
			out = append(out, displayPath(p))
		}
	}
	return out
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
