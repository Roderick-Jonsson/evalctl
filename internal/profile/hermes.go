package profile

// The harness contract: evalctl launches `hermes chat` with a fixed set of flags and reads the
// stream-json frames that come back. Both halves are somebody else's interface, so both are checked
// out loud rather than discovered by a run that produces nothing:
//
//   - Probe asks the installed binary whether its `chat` verb still takes the flags evalctl passes.
//     A renamed flag is a refusal with the missing flags named, not an arm that exits 2 into a file
//     nobody reads.
//   - StreamProblem reads one finished launch and says whether it produced anything the harness can
//     score, naming a sign-in failure when the stderr says that is what happened.

import (
	"fmt"
	"os/exec"
	"strings"
)

// RequiredChatFlags are the flags every evalctl launch passes. --max-turns is the trigger lane's; the
// rest are on every arm. If hermes grows a different spelling, this list is the one place to change.
var RequiredChatFlags = []string{"--oneshot", "--query-file", "--format", "--in", "--yolo", "--max-turns"}

// Probe reports the hermes binary and the flags its `chat` verb does not advertise. It is cheap
// (one local `--help`), it never launches a model, and it runs before the first sandbox is created.
func (m *Manager) Probe() (string, error) {
	path, err := exec.LookPath(m.Bin)
	if err != nil {
		return "", fmt.Errorf("hermes not found (%s): install Hermes, or point HERMES_BIN at the binary "+
			"(`evalctl profiles status` needs it too)", m.Bin)
	}
	// A --help that exits non-zero but prints is still an answer: only empty output means the probe
	// learned nothing.
	out, _ := exec.Command(path, "chat", "--help").CombinedOutput()
	text := string(out)
	if strings.TrimSpace(text) == "" {
		return path, fmt.Errorf("%s chat --help printed nothing — evalctl cannot tell whether this hermes "+
			"takes the flags it launches with (%s); run `hermes version` and upgrade", path, strings.Join(RequiredChatFlags, " "))
	}
	var missing []string
	for _, flag := range RequiredChatFlags {
		if !strings.Contains(text, flag) {
			missing = append(missing, flag)
		}
	}
	if len(missing) > 0 {
		return path, fmt.Errorf("this hermes does not accept %s on `chat` — evalctl drives `hermes chat` with "+
			"%s and reads stream-json frames; check `hermes version` (found %s) or set HERMES_BIN to a "+
			"compatible build", strings.Join(missing, ", "), strings.Join(RequiredChatFlags, " "), path)
	}
	return path, nil
}

// HermesVersion is the installed binary's own version line, or "" when it cannot be asked.
func (m *Manager) HermesVersion() string {
	out, err := exec.Command(m.Bin, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// SignInHints are the stderr fragments a launch prints when it has no working credential. They are
// matched case-insensitively; none of them is proof, which is why the original line is quoted too.
var SignInHints = []string{
	"not logged in", "no credentials", "unauthorized", "authentication", "invalid api key",
	"api key is", "missing api key", "401", "403", "please log in", "run `hermes",
}

// SignInProblem reports the credential failure behind a stderr, or nil when it says nothing about
// one. The first matching line is quoted, so the reader sees the harness's own words.
func SignInProblem(stderr string) error {
	lower := strings.ToLower(stderr)
	if strings.TrimSpace(lower) == "" {
		return nil
	}
	for _, hint := range SignInHints {
		if !strings.Contains(lower, hint) {
			continue
		}
		return fmt.Errorf("the launch could not sign in — %s\n  the eval sandbox gets the real profile's "+
			".env keys and login files; `evalctl profiles check` says what it has", firstLine(stderr, hint))
	}
	return nil
}

// firstLine is the stderr line that carries the hint, cut to one readable line.
func firstLine(stderr, hint string) string {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(strings.ToLower(line), hint) {
			if len(line) > 240 {
				line = line[:240] + "…"
			}
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])
}
