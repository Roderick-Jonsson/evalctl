package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/model"
)

// The description is the router's input: it is the only part of a SKILL.md an agent sees before it
// decides to load the skill, and the agentskills.io optimization loop tunes nothing else. So it gets
// a reader and a writer of its own, in one place, instead of a regex in the TUI and another in the
// report.

// DescriptionsDir is where each skill's candidate ledger lives, beside the trigger set it is
// measured against.
const DescriptionsDir = "descriptions"

// DescriptionLimit is the format's hard ceiling on a description.
const DescriptionLimit = 1024

// DescriptionPath is one skill's ledger file.
func (s *Store) DescriptionPath(skill string) string {
	return filepath.Join(s.Root, "trigger", DescriptionsDir, skill+".json")
}

// LoadDescriptionLedger reads one skill's ledger. A missing file is not an error: every skill the
// tool has ever seen starts with no loop at all, and the first candidate is copied from SKILL.md.
func (s *Store) LoadDescriptionLedger(skill string) (model.DescriptionLedger, bool, error) {
	path := s.DescriptionPath(skill)
	ledger := model.DescriptionLedger{Skill: skill}
	if !isFile(path) {
		return ledger, false, nil
	}
	if err := readJSON(path, &ledger); err != nil {
		return ledger, true, fmt.Errorf("%s: %w", rel(s.Root, path), err)
	}
	if ledger.Skill == "" {
		ledger.Skill = skill
	}
	return ledger, true, nil
}

// SaveDescriptionLedger writes one skill's ledger, refusing the shapes the lab cannot read back:
// two candidates sharing an id, an empty draft, a skill name that is not this file's.
func (s *Store) SaveDescriptionLedger(l model.DescriptionLedger) error {
	if strings.TrimSpace(l.Skill) == "" {
		return fmt.Errorf("ledger has no skill name")
	}
	seen := map[int]bool{}
	for _, c := range l.Candidates {
		if c.ID <= 0 {
			return fmt.Errorf("%s: candidate id must be positive", l.Skill)
		}
		if seen[c.ID] {
			return fmt.Errorf("%s: candidate %d appears twice", l.Skill, c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Text) == "" {
			return fmt.Errorf("%s: candidate %d has no text", l.Skill, c.ID)
		}
	}
	for _, c := range l.Candidates {
		if c.Parent != 0 && !seen[c.Parent] {
			return fmt.Errorf("%s: candidate %d revises %d, which is not in the ledger", l.Skill, c.ID, c.Parent)
		}
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(s.DescriptionPath(l.Skill), append(b, '\n'))
}

// NextDescriptionCandidateID is the next free number in a skill's ledger.
func (s *Store) NextDescriptionCandidateID(skill string) int {
	ledger, _, err := s.LoadDescriptionLedger(skill)
	if err != nil {
		return 1
	}
	best := 0
	for _, c := range ledger.Candidates {
		if c.ID > best {
			best = c.ID
		}
	}
	return best + 1
}

// DescriptionSkills is every skill that has a ledger on disk, alphabetically.
func (s *Store) DescriptionSkills() []string {
	entries, err := os.ReadDir(filepath.Join(s.Root, "trigger", DescriptionsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- SKILL.md frontmatter

// SkillDescription is the `description:` value in a skill's SKILL.md frontmatter, block scalars
// folded flat — the text the router actually reads, not the bytes it is written in.
func (s *Store) SkillDescription(skill string) string {
	b, err := os.ReadFile(filepath.Join(s.Root, skill, "SKILL.md"))
	if err != nil {
		return ""
	}
	value, _, _, _ := DescriptionSpan(string(b))
	return value
}

// SkillDescriptionHash is the identity of the description under test. Trigger iterations record the
// text they ran; the lab joins on this so two candidates that read the same are one measurement.
func (s *Store) SkillDescriptionHash(skill string) string { return HashText(s.SkillDescription(skill)) }

// SetSkillDescription rewrites the `description:` value in a skill's SKILL.md and nothing else: the
// frontmatter's other keys, the body, and the file's line endings all keep their bytes. The old file
// goes to .evalctl/backups/ like every other write.
//
// It reads the value back afterwards and compares: a folded block scalar that came back different
// from what was asked for is a description the router would read as something else, and finding that
// out from a trigger run twenty minutes later is the failure this check exists to prevent.
func (s *Store) SetSkillDescription(skill, text string) error {
	path := filepath.Join(s.Root, skill, "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	want := FoldText(text)
	if n := len([]rune(want)); n > DescriptionLimit {
		return fmt.Errorf("description is %d chars — the limit is %d", n, DescriptionLimit)
	}
	if want == "" {
		return fmt.Errorf("description is empty")
	}
	_, start, end, ok := DescriptionSpan(string(b))
	if !ok {
		return fmt.Errorf("%s/SKILL.md has no `description:` in its frontmatter", skill)
	}
	next := string(b)[:start] + renderDescription(want) + string(b)[end:]
	if err := s.writeFile(path, []byte(next)); err != nil {
		return err
	}
	if got := s.SkillDescription(skill); got != want {
		return fmt.Errorf("SKILL.md re-read does not match the description written — the file is %q", clipText(got, 60))
	}
	return nil
}

// DescriptionSpan finds the description value in a SKILL.md and the bytes its lines occupy, so a
// rewrite replaces exactly those bytes. start is the first byte of the `description:` line and end
// is one past the last newline of the block.
func DescriptionSpan(text string) (value string, start, end int, ok bool) {
	type line struct {
		start, end int // end is one past the newline
		body       string
	}
	var lines []line
	for i := 0; i < len(text); {
		nl := strings.IndexByte(text[i:], '\n')
		if nl < 0 {
			lines = append(lines, line{start: i, end: len(text), body: text[i:]})
			break
		}
		lines = append(lines, line{start: i, end: i + nl + 1, body: text[i : i+nl]})
		i += nl + 1
	}
	if len(lines) < 2 || strings.TrimSpace(lines[0].body) != "---" {
		return "", 0, 0, false
	}
	close := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i].body) == "---" {
			close = i
			break
		}
	}
	if close < 0 {
		return "", 0, 0, false
	}
	for i := 1; i < close; i++ {
		trimmed := strings.TrimSpace(lines[i].body)
		if !strings.HasPrefix(trimmed, "description:") || lines[i].body != trimmed {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
		switch rest {
		case "|", ">", "|-", ">-", "|+", ">+":
			var parts []string
			last := i
			for j := i + 1; j < close; j++ {
				body := lines[j].body
				if body == "" {
					continue // a paragraph break inside the block is still inside the block
				}
				if body[0] == ' ' || body[0] == '	' {
					if part := strings.TrimSpace(body); part != "" {
						parts = append(parts, part)
					}
					last = j
					continue
				}
				break
			}
			return strings.Join(parts, " "), lines[i].start, lines[last].end, true
		}
		return strings.Trim(rest, "\"'"), lines[i].start, lines[i].end, true
	}
	return "", 0, 0, false
}

// renderDescription writes a value as one YAML line when it is short, and as a folded block where
// the file is read by a human, because a 400-character single line in a frontmatter is a line nobody
// reviews. Folding turns each newline back into a space, so the value round-trips.
func renderDescription(text string) string {
	if n := len([]rune(text)); n <= 96 {
		return "description: " + text + "\n"
	}
	var b strings.Builder
	b.WriteString("description: >\n")
	for _, line := range wrapWords(text, 78) {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

// wrapWords cuts on spaces and never inside a word: a path or a URL is one token to the reader.
func wrapWords(text string, width int) []string {
	var out []string
	var line strings.Builder
	for _, word := range strings.Fields(text) {
		switch {
		case line.Len() == 0:
			line.WriteString(word)
		case line.Len()+1+len([]rune(word)) <= width:
			line.WriteString(" ")
			line.WriteString(word)
		default:
			out = append(out, line.String())
			line.Reset()
			line.WriteString(word)
		}
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// FoldText is the text a folded block scalar reads back as: whitespace runs collapse to one space,
// which is precisely how the router receives a description typed as a paragraph.
func FoldText(text string) string { return strings.Join(strings.Fields(text), " ") }

// HashText is the identity of one description wording: what a trigger iteration recorded it ran,
// and what a candidate's measurements are joined on.
func HashText(text string) string {
	sum := sha256.Sum256([]byte(FoldText(text)))
	return hex.EncodeToString(sum[:])
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
