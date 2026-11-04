package diff

// Hunks turn a flat edit script into what a reviewer actually reads: the changed neighbourhoods,
// with the untouched middle folded away. It is the difference between a skill's whole text and the
// four places a reviser touched it.
//
// The shape is the one every diff tool converged on — a contiguous run of changes plus `context`
// lines either side, changes closer together than that merged into one hunk — because a reader
// scanning for "what did it do" should not have to walk a file to find out.

// Hunk is one changed neighbourhood: where it starts on each side, and the lines it covers.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	// From and To index the Lines slice the hunk was cut from, so a caller can map a hunk back to the
	// script without re-deriving it.
	From, To int
	Lines    []Line
	// Changes is how many lines of the hunk are added or removed: a hunk made only of context is not
	// a hunk, and this is what lets a screen say "3 changes in 2 hunks".
	Changes int
}

// Hunks cuts an edit script into changed neighbourhoods with `context` lines of overlap. A script
// with no changes has no hunks.
func Hunks(lines []Line, context int) []Hunk {
	if context < 0 {
		context = 0
	}
	// Where the changes are, so a run of them plus context becomes one window.
	var changed []int
	for i, l := range lines {
		if l.Kind != Same {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	var out []Hunk
	start := max(0, changed[0]-context)
	for i := 1; i <= len(changed); i++ {
		// A change starts a new hunk when the gap since the previous one is wider than the context
		// both sides can afford; otherwise the windows would overlap and read as one already.
		if i == len(changed) || changed[i]-changed[i-1] > 2*context+1 {
			end := min(len(lines), changed[i-1]+context+1)
			out = append(out, makeHunk(lines, start, end))
			if i < len(changed) {
				start = max(0, changed[i]-context)
			}
		}
	}
	return out
}

func makeHunk(lines []Line, from, to int) Hunk {
	h := Hunk{From: from, To: to, Lines: append([]Line(nil), lines[from:to]...)}
	// Count the line numbers each side stands at before the window opens.
	oldNo, newNo := 1, 1
	for _, l := range lines[:from] {
		switch l.Kind {
		case Del:
			oldNo++
		case Add:
			newNo++
		default:
			oldNo, newNo = oldNo+1, newNo+1
		}
	}
	h.OldStart, h.NewStart = oldNo, newNo
	for _, l := range h.Lines {
		switch l.Kind {
		case Del:
			h.OldCount++
			h.Changes++
		case Add:
			h.NewCount++
			h.Changes++
		default:
			h.OldCount++
			h.NewCount++
		}
	}
	return h
}

// Apply rebuilds a text from the script, taking the after-side of the hunks the caller selected and
// the before-side of the rest. Selecting nothing gives the original back; selecting everything gives
// the proposal. That is what makes a partial accept a real edit rather than a copy of half a diff.
func Apply(lines []Line, hunks []Hunk, selected func(hunk int) bool) string {
	inHunk := make([]int, len(lines))
	for i := range inHunk {
		inHunk[i] = -1
	}
	for i, h := range hunks {
		for j := h.From; j < h.To && j < len(lines); j++ {
			inHunk[j] = i
		}
	}
	var out []string
	for i, l := range lines {
		take := true
		if h := inHunk[i]; h >= 0 {
			take = selected(h)
		}
		switch l.Kind {
		case Add:
			if take {
				out = append(out, l.Text)
			}
		case Del:
			if !take {
				out = append(out, l.Text)
			}
		default:
			out = append(out, l.Text)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return join(out)
}

func join(lines []string) string {
	var b []byte
	for i, l := range lines {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, l...)
	}
	return string(b) + "\n"
}

// StatOf is Stat for a subset of hunks: what the selected edits add and remove.
func StatOf(hunks []Hunk, selected func(hunk int) bool) (added, removed int) {
	for i, h := range hunks {
		if selected != nil && !selected(i) {
			continue
		}
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				added++
			case Del:
				removed++
			}
		}
	}
	return added, removed
}
