//go:build windows

package theme

// Windows has no OSC 11 read on a redirected handle and no ioctl to put the console in raw mode, so
// the background is taken from the environment and from the palette's default. Answering "unknown"
// is honest: the dark palette is the default and COLORFGBG still wins when a shell sets it.

import "time"

// queryBackground always says "no answer" on Windows.
func queryBackground(d time.Duration) (r, g, b float64, ok bool) {
	return 0, 0, 0, false
}
