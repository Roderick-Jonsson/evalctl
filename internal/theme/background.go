//go:build !windows

package theme

import (
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// backgroundQuery is the OSC 11 sequence: "what colour is your background?". Terminals that
// support it answer with `ESC ] 11 ; rgb:RRRR/GGGG/BBBB`, terminated by BEL or ST.
const backgroundQuery = "\x1b]11;?\x1b\\"

// queryBackground asks the terminal for its background colour, waiting at most d for the answer.
//
// The tty is put in raw mode for the exchange and handed back exactly as it was found, because
// the TUI takes it over next and a mode left half-set would break every key. A terminal that
// ignores the question simply never answers, and the caller keeps its default.
func queryBackground(d time.Duration) (r, g, b float64, ok bool) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		fd = int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			return 0, 0, 0, false
		}
	}
	saved, err := term.MakeRaw(fd)
	if err != nil {
		return 0, 0, 0, false
	}
	defer term.Restore(fd, saved) //nolint:errcheck // nothing left to do if the mode cannot go back

	if _, err := os.Stdout.WriteString(backgroundQuery); err != nil {
		return 0, 0, 0, false
	}
	deadline := time.Now().Add(d)
	var read []byte
	for time.Now().Before(deadline) {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 25)
		if err != nil && err != unix.EINTR {
			return 0, 0, 0, false
		}
		if n == 0 {
			continue
		}
		buf := make([]byte, 64)
		k, err := unix.Read(fd, buf)
		if err != nil || k <= 0 {
			return 0, 0, 0, false
		}
		read = append(read, buf[:k]...)
		if r, g, b, ok := parseBackground(string(read)); ok {
			return r, g, b, true
		}
	}
	return 0, 0, 0, false
}

// parseBackground pulls the components out of an OSC 11 reply. xterm writes 16-bit components
// (rgb:0000/0000/0000), but 8-bit and 4-digit variants exist, so each component is scaled by its
// own width rather than assumed. Values are returned as linear-light 0–1.
func parseBackground(s string) (r, g, b float64, ok bool) {
	at := strings.Index(s, "\x1b]11;")
	if at < 0 {
		return 0, 0, 0, false
	}
	rest := s[at+len("\x1b]11;"):]
	end := strings.IndexAny(rest, "\x07\x1b")
	if end < 0 {
		return 0, 0, 0, false
	}
	spec := rest[:end]
	spec = strings.TrimPrefix(strings.TrimPrefix(spec, "rgb:"), "rgba:")
	parts := strings.Split(spec, "/")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	out := make([]float64, 3)
	for i, part := range parts[:3] {
		v, err := strconv.ParseUint(strings.TrimSpace(part), 16, 32)
		if err != nil || len(part) == 0 {
			return 0, 0, 0, false
		}
		scale := float64((uint64(1) << (4 * len(part))) - 1)
		out[i] = linear(float64(v) / scale * 255)
	}
	return out[0], out[1], out[2], true
}
