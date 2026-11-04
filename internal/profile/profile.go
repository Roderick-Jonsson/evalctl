// Package profile provisions the Hermes profiles the eval sandboxes run in — the Go port of
// Python's profiles.py, which it replaced once the port matched.
//
// One contract, four profiles: the real one and three sandboxes (with-skill, baseline,
// trigger). `contract.md` is the only hand-written source of profile state; `Render` stamps it
// into each profile's SOUL.md under the Hermes persona and records the contract hash there, so
// the two arms are provably byte-identical and nobody edits a profile by hand.
//
// The workspace root is the run's working directory (the contract's Workspace rule), so where
// production masterclasses live is a provisioning fact, not contract text: `PinCwd` pins the
// real profile's terminal.cwd and leaves the sandboxes unpinned (the harness's --in decides).
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/stage"
)

const (
	Real    = "video-course-creator"
	With    = "eval-vcc-with"
	Without = "eval-vcc-without"
	Trigger = "eval-vcc-trigger"
	// Revise writes the proposal the loop's step 1 asks for: an agent that reads an iteration's
	// failures and feedback and answers with a new SKILL.md. It is a sandbox of its own because the
	// arms' skills would let it read the evals it is being measured against, and the judge's framing
	// would have it grading instead of writing.
	Revise = "eval-vcc-revise"
)

// EvalProfiles are the throwaway sandboxes: memories are wiped between iterations, and none of
// them may pin a working directory.
var EvalProfiles = []string{With, Without, Trigger, Revise}

// SkillParts are the parts of a stage skill that get installed. evals/, .git and .DS_Store stay
// behind — the installed skill must run, not test.
var SkillParts = []string{"SKILL.md", "scripts", "references", "assets", "templates"}

// EnvKeys is what a sandbox needs to run the stage pipeline. The rest of the default .env
// (messaging bot tokens, Notion) stays out of the sandboxes.
var EnvKeys = []string{"OLLAMA_API_KEY"}

// PipelineOrder is the stage order the contract defines. Skills are listed in it, not
// alphabetically, because "the skill under test" (and the with-arm's steady state) means the first
// stage — sorting by name would make av-production the default arm. The list itself lives in
// internal/stage, beside each stage's own outputs, so the harness has one idea of the pipeline.
var PipelineOrder = stage.Order

// CwdSentinels are values Hermes treats as "terminal.cwd not configured".
var CwdSentinels = map[string]bool{"": true, ".": true, "./": true, "auto": true, "cwd": true}

const contractHead = "# video-course-creator — stage contract"

const (
	markerOpen = "<!-- stage-contract "
	markerTail = " source=contract.md -->"
)

// Manager owns the provisioning rules for one tree.
type Manager struct {
	Root            string   // the dir holding contract.md and the stage skills
	Hermes          string   // ~/.hermes, or $HERMES_ROOT
	Skills          []string // stage skills, sorted
	MasterclassRoot string   // the production workspace root
	Bin             string   // the hermes executable
}

// New builds a manager for root. `skills` are the stage skills the tree declares.
func New(root string, skills []string) *Manager {
	hermes := os.Getenv("HERMES_ROOT")
	if hermes == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			hermes = filepath.Join(home, ".hermes")
		}
	}
	master := os.Getenv("EVAL_MASTERCLASS_ROOT")
	if master == "" {
		if home, err := os.UserHomeDir(); err == nil {
			master = filepath.Join(home, "Documents", "4-Interests", "masterclasses")
		}
	}
	bin := os.Getenv("HERMES_BIN")
	if bin == "" {
		bin = "hermes"
	}
	return &Manager{Root: root, Hermes: hermes, Skills: SortPipeline(skills), MasterclassRoot: master, Bin: bin}
}

// SortPipeline orders stage skills by the contract's stage order, unknown names last (alphabetical).
func SortPipeline(skills []string) []string {
	rank := map[string]int{}
	for i, name := range PipelineOrder {
		rank[name] = i
	}
	out := append([]string(nil), skills...)
	sort.Slice(out, func(i, j int) bool {
		ri, iok := rank[out[i]]
		rj, jok := rank[out[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		return out[i] < out[j]
	})
	return out
}

func (m *Manager) ContractPath() string { return filepath.Join(m.Root, "contract.md") }

// Home is the profile dir: the Hermes root itself for "default", a subdir otherwise.
func (m *Manager) Home(name string) string {
	if name == "default" {
		return m.Hermes
	}
	return filepath.Join(m.Hermes, "profiles", name)
}

func (m *Manager) Soul(name string) string   { return filepath.Join(m.Home(name), "SOUL.md") }
func (m *Manager) SkillsDir(n string) string { return filepath.Join(m.Home(n), "skills") }

// Contract reads the contract and its hash — the one hand-written input.
func (m *Manager) Contract() (string, string, error) {
	b, err := os.ReadFile(m.ContractPath())
	if err != nil {
		return "", "", fmt.Errorf("contract: %w", err)
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// Plan is the steady state of each profile. The with-skill arm is re-synced per case, so its
// entry is one skill rather than a fixed contents list.
func (m *Manager) Plan(name string) []string {
	switch name {
	case Real, Trigger:
		return m.Skills
	case With:
		if len(m.Skills) > 0 {
			return m.Skills[:1]
		}
		return nil
	default:
		return nil
	}
}

// Descriptions are the profile-level one-liners Hermes shows when picking a profile.
func Descriptions() map[string]string {
	return map[string]string{
		Real:    "Builds a masterclass from source material: research, learning path, slides, video.",
		With:    "Eval sandbox — with-skill arm. Not for real work; provisioned by evalctl.",
		Without: "Eval sandbox — baseline arm, no skills. Not for real work; provisioned by evalctl.",
		Trigger: "Eval sandbox — trigger arm, all four stage skills. Not for real work.",
		Revise:  "Eval sandbox — writes a skill revision from one iteration's failures. Not for real work.",
	}
}

// ---------------------------------------------------------------- SOUL.md

// Persona is SOUL.md as it was before any contract was stamped into it. It cuts at the first
// sign of a previous render — the stamp comment or the contract heading — so re-rendering
// replaces the block instead of appending a second copy to it.
func (m *Manager) Persona(name string) string {
	b, err := os.ReadFile(m.Soul(name))
	if err != nil {
		return ""
	}
	text := string(b)
	cut := len(text)
	for _, token := range []string{markerOpen, contractHead} {
		if found := strings.Index(text, token); found != -1 && found < cut {
			cut = found
		}
	}
	return strings.TrimSpace(text[:cut])
}

// SoulText renders persona + contract block, byte-identically for every profile.
func (m *Manager) SoulText(name string) (string, error) {
	body, digest, err := m.Contract()
	if err != nil {
		return "", err
	}
	base := m.Persona(name)
	block := fmt.Sprintf("<!-- stage-contract sha256=%s source=contract.md -->\n%s\n\n%s",
		digest, contractHead, contractBody(body))
	text := block
	if base != "" {
		text = base + "\n\n" + block
	}
	return strings.TrimRight(text, " \t\r\n\v\f") + "\n", nil
}

// contractBody is Python's `body.split("\n", 2)[2].lstrip()`: everything after the second line.
func contractBody(body string) string {
	parts := strings.SplitN(body, "\n", 3)
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimLeftFunc(parts[2], isSpace)
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

// Render stamps the contract into one profile's SOUL.md.
func (m *Manager) Render(name string, dry bool) (string, error) {
	_, digest, err := m.Contract()
	if err != nil {
		return "", err
	}
	text, err := m.SoulText(name)
	if err != nil {
		return "", err
	}
	stamps, heads := strings.Count(text, markerOpen), strings.Count(text, contractHead)
	if stamps != 1 || heads != 1 {
		return "", fmt.Errorf("render %s: refusing — SOUL.md would hold %d contract copies "+
			"(%d stamps); the persona cut is wrong", name, heads, stamps)
	}
	msg := fmt.Sprintf("render %s/SOUL.md (contract %s, %d bytes)", name, short(digest), len(text))
	if dry {
		return msg, nil
	}
	path := m.Soul(name)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		return "", err
	}
	_ = os.Chmod(path, mode)
	return msg, nil
}

// SoulSHA fingerprints the rendered SOUL.md — the proof both arms saw the same contract.
func (m *Manager) SoulSHA(name string) *string {
	b, err := os.ReadFile(m.Soul(name))
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(b)
	hexed := hex.EncodeToString(sum[:])
	return &hexed
}

func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

// ---------------------------------------------------------------- .env

func parseEnvKeys(b []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

// DefaultEnv is the real profile's .env, the only source of credentials for the sandboxes.
func (m *Manager) DefaultEnv() map[string]string {
	b, err := os.ReadFile(filepath.Join(m.Hermes, ".env"))
	if err != nil {
		return map[string]string{}
	}
	return parseEnvKeys(b)
}

// EnvKeysIn lists the keys a profile's .env already carries.
func (m *Manager) EnvKeysIn(name string) []string {
	b, err := os.ReadFile(filepath.Join(m.Home(name), ".env"))
	if err != nil {
		return nil
	}
	keys := make([]string, 0)
	for k := range parseEnvKeys(b) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteEnv copies the credential keys into a profile's .env, creating it only when incomplete.
func (m *Manager) WriteEnv(name string, dry bool) (string, error) {
	source := m.DefaultEnv()
	var missing []string
	for _, k := range EnvKeys {
		if _, ok := source[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("env %s: SKIPPED — %s not in %s", name, strings.Join(missing, ", "),
			filepath.Join(m.Hermes, ".env")), nil
	}
	have := map[string]bool{}
	for _, k := range m.EnvKeysIn(name) {
		have[k] = true
	}
	complete := true
	for _, k := range EnvKeys {
		if !have[k] {
			complete = false
		}
	}
	if complete {
		return fmt.Sprintf("env %s: already has %s", name, strings.Join(EnvKeys, ", ")), nil
	}
	msg := fmt.Sprintf("env %s/.env ← %s", name, strings.Join(EnvKeys, ", "))
	body := []string{"# Provisioned by evalctl — credentials only, settings belong in config.yaml.", ""}
	for _, k := range EnvKeys {
		body = append(body, k+"="+source[k])
	}
	if dry {
		return msg, nil
	}
	path := filepath.Join(m.Home(name), ".env")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.Join(body, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(path, 0o600)
	return msg, nil
}

// ---------------------------------------------------------------- skills

// TreeHash fingerprints the parts that actually get installed — the unit a run sees.
func (m *Manager) TreeHash(skill string) (string, error) {
	digest := sha256.New()
	root := filepath.Join(m.Root, skill)
	for _, part := range SkillParts {
		path := filepath.Join(root, part)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			digest.Write([]byte(part + "\x00"))
			digest.Write(b)
			continue
		}
		var files []string
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Strings(files)
		for _, rel := range files {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return "", err
			}
			digest.Write([]byte(rel + "\x00"))
			digest.Write(b)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// InstalledSkills lists what a profile's skills/ holds right now.
func (m *Manager) InstalledSkills(name string) []string {
	entries, err := os.ReadDir(m.SkillsDir(name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// CopySkill installs one stage skill without its evals, .git or .DS_Store.
func (m *Manager) CopySkill(skill string, destRoot string) error {
	src := filepath.Join(m.Root, skill)
	if info, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil || info.IsDir() {
		return fmt.Errorf("%s: no SKILL.md at %s", skill, src)
	}
	dest := filepath.Join(destRoot, skill)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, part := range SkillParts {
		source := filepath.Join(src, part)
		info, err := os.Stat(source)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if err := copyFile(source, filepath.Join(dest, part)); err != nil {
				return err
			}
			continue
		}
		err = filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if d.Name() == ".DS_Store" {
				return nil
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if hasPart(rel, ".git") || hasPart(rel, "evals") {
				return nil
			}
			target := filepath.Join(dest, filepath.FromSlash(rel))
			return copyFile(p, target)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func hasPart(path, want string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == want {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		if err := os.WriteFile(dst, b, info.Mode().Perm()); err != nil {
			return err
		}
		return nil
	}
	return os.WriteFile(dst, b, 0o644)
}

// SyncSkills makes skills/ hold exactly `wanted` — anything else is removed.
func (m *Manager) SyncSkills(name string, wanted []string, dry bool) (string, error) {
	for _, w := range wanted {
		if !m.IsSkill(w) {
			return "", fmt.Errorf("unknown skill %q (known: %s)", w, strings.Join(m.Skills, ", "))
		}
	}
	current := m.InstalledSkills(name)
	if dry {
		return fmt.Sprintf("skills %s: %s → %s", name, orDash(current), orDash(wanted)), nil
	}
	dir := m.SkillsDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	keep := map[string]bool{}
	for _, w := range wanted {
		keep[w] = true
	}
	for _, have := range current {
		if !keep[have] {
			if err := os.RemoveAll(filepath.Join(dir, have)); err != nil {
				return "", err
			}
		}
	}
	for _, w := range wanted {
		target := filepath.Join(dir, w)
		if _, err := os.Stat(target); err == nil {
			if err := os.RemoveAll(target); err != nil {
				return "", err
			}
		}
		if err := m.CopySkill(w, dir); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("skills %s: %s", name, orDash(wanted)), nil
}

// IsSkill reports whether name is one of the tree's stage skills.
func (m *Manager) IsSkill(name string) bool {
	for _, s := range m.Skills {
		if s == name {
			return true
		}
	}
	return false
}

func orDash(names []string) string {
	if len(names) == 0 {
		return "[—]"
	}
	return "[" + strings.Join(names, " ") + "]"
}

// Scrub clears agent-written state in a sandbox. SOUL.md is never touched.
func (m *Manager) Scrub(name string, dry bool) (string, error) {
	if name != With && name != Without && name != Trigger && name != Revise {
		return fmt.Sprintf("scrub %s: refused — only eval sandboxes are wiped", name), nil
	}
	mem := filepath.Join(m.Home(name), "memories")
	entries, err := os.ReadDir(mem)
	if err != nil {
		return fmt.Sprintf("scrub %s: memories clean", name), nil
	}
	var wiped []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		wiped = append(wiped, e.Name())
		if !dry {
			_ = os.Remove(filepath.Join(mem, e.Name()))
		}
	}
	if len(wiped) == 0 {
		return fmt.Sprintf("scrub %s: memories clean", name), nil
	}
	sort.Strings(wiped)
	return fmt.Sprintf("scrub %s: memories %s", name, strings.Join(wiped, ", ")), nil
}

// ---------------------------------------------------------------- working directory

// TerminalCwd reads the profile's configured terminal.cwd ("" when unset or a sentinel).
func (m *Manager) TerminalCwd(name string) string {
	b, err := os.ReadFile(filepath.Join(m.Home(name), "config.yaml"))
	if err != nil {
		return ""
	}
	inTerminal := false
	for _, line := range strings.Split(string(b), "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inTerminal = stripped == "terminal:"
			continue
		}
		if inTerminal && strings.HasPrefix(stripped, "cwd:") {
			value := strings.TrimSpace(strings.TrimPrefix(stripped, "cwd:"))
			value = strings.Trim(value, "\"'")
			if CwdSentinels[strings.ToLower(value)] {
				return ""
			}
			return value
		}
	}
	return ""
}

// PinCwd pins the real profile's working directory to the production root, through
// `hermes config set` — never by hand-editing config.yaml.
func (m *Manager) PinCwd(name string, dry bool) (string, error) {
	if name != Real {
		return fmt.Sprintf("cwd %s: refused — only %s pins a working directory", name, Real), nil
	}
	current := m.TerminalCwd(name)
	target := m.MasterclassRoot
	if current == target {
		return fmt.Sprintf("cwd %s: already pinned to %s", name, target), nil
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return "", fmt.Errorf("cwd %s: %s does not exist — create it first", name, target)
	}
	if dry {
		return fmt.Sprintf("cwd %s: %s → %s", name, orUnset(current), target), nil
	}
	out, err := exec.Command(m.Bin, "-p", name, "config", "set", "terminal.cwd", target).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes config set terminal.cwd failed for %s:\n%s", name, out)
	}
	return fmt.Sprintf("cwd %s: %s → %s", name, orUnset(current), target), nil
}

func orUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

// ---------------------------------------------------------------- profiles

// Create makes the profile with no skills and no alias.
func (m *Manager) Create(name string, dry bool) (string, error) {
	if info, err := os.Stat(m.Home(name)); err == nil && info.IsDir() {
		return fmt.Sprintf("create %s: exists", name), nil
	}
	if dry {
		return fmt.Sprintf("create %s (hermes profile create --no-skills --no-alias)", name), nil
	}
	desc := Descriptions()[name]
	out, err := exec.Command(m.Bin, "profile", "create", name, "--no-skills", "--no-alias",
		"--description", desc).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes profile create %s failed:\n%s", name, out)
	}
	return "create " + name, nil
}

// Ensure brings one profile to its intended state: created, described, env, SOUL, skills, cwd,
// and — for sandboxes — memories wiped.
func (m *Manager) Ensure(name string, dry bool) ([]string, error) {
	var steps []string
	line, err := m.Create(name, dry)
	if err != nil {
		return nil, err
	}
	steps = append(steps, line)
	if _, err := os.Stat(m.Home(name)); err == nil || dry {
		if !dry {
			_ = exec.Command(m.Bin, "profile", "describe", name, "--text", Descriptions()[name]).Run()
		}
	}
	for _, step := range []func(string, bool) (string, error){m.WriteEnv, m.Render} {
		line, err := step(name, dry)
		if err != nil {
			return nil, err
		}
		steps = append(steps, line)
	}
	line, err = m.SyncSkills(name, m.Plan(name), dry)
	if err != nil {
		return nil, err
	}
	steps = append(steps, line)
	line, err = m.PinCwd(name, dry)
	if err != nil {
		return nil, err
	}
	steps = append(steps, line)
	if name != Real {
		line, err = m.Scrub(name, dry)
		if err != nil {
			return nil, err
		}
		steps = append(steps, line)
	}
	return steps, nil
}

// Status is one profile's state, as `check` judges it.
type Status struct {
	Profile         string   `json:"profile"`
	Exists          bool     `json:"exists"`
	SoulSHA         *string  `json:"soul_sha256"`
	ContractStamp   *string  `json:"contract_stamp"`
	ContractCurrent bool     `json:"contract_current"`
	Skills          []string `json:"skills"`
	SkillsOK        bool     `json:"skills_ok"`
	Memories        []string `json:"memories"`
	EnvKeys         []string `json:"env_keys"`
	Model           string   `json:"model"`
	TerminalCwd     string   `json:"terminal_cwd"`
}

// Status reports one profile's state.
func (m *Manager) Status(name string) Status {
	_, digest, _ := m.Contract()
	b, err := os.ReadFile(m.Soul(name))
	text := ""
	if err == nil {
		text = string(b)
	}
	var stamp *string
	if i := strings.Index(text, markerOpen); i != -1 {
		rest := text[i+len(markerOpen):]
		if j := strings.Index(rest, markerTail); j != -1 {
			value := rest[:j]
			value = strings.TrimPrefix(value, "sha256=")
			stamp = &value
		}
	}
	skills := m.InstalledSkills(name)
	skillsOK := sameSet(skills, m.Plan(name))
	if name == With {
		skillsOK = len(skills) == 1
	}
	var memories []string
	if entries, err := os.ReadDir(filepath.Join(m.Home(name), "memories")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				memories = append(memories, e.Name())
			}
		}
		sort.Strings(memories)
	}
	exists := false
	if info, err := os.Stat(m.Home(name)); err == nil && info.IsDir() {
		exists = true
	}
	return Status{
		Profile:         name,
		Exists:          exists,
		SoulSHA:         m.SoulSHA(name),
		ContractStamp:   stamp,
		ContractCurrent: stamp != nil && *stamp == digest,
		Skills:          skills,
		SkillsOK:        skillsOK,
		Memories:        memories,
		EnvKeys:         m.EnvKeysIn(name),
		Model:           modelDefault(m.Home(name)),
		TerminalCwd:     m.TerminalCwd(name),
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func modelDefault(home string) string {
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "default:") {
			_, value, _ := strings.Cut(line, "default:")
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Check reports every deviation from the intended state, and the rows it judged.
func (m *Manager) Check(names []string) ([]Status, []string) {
	rows := make([]Status, 0, len(names))
	var problems []string
	_, digest, _ := m.Contract()
	for _, name := range names {
		row := m.Status(name)
		rows = append(rows, row)
		if !row.Exists {
			problems = append(problems, name+": profile missing")
			continue
		}
		switch {
		case row.ContractStamp == nil:
			problems = append(problems, name+": SOUL.md has no contract stamp")
		case !row.ContractCurrent:
			problems = append(problems, fmt.Sprintf("%s: SOUL.md holds a stale contract (%s ≠ %s) — run render",
				name, short(*row.ContractStamp), short(digest)))
		}
		if !row.SkillsOK {
			problems = append(problems, fmt.Sprintf("%s: skills %v ≠ %v", name, row.Skills, m.Plan(name)))
		}
		if name != Real && len(row.Memories) > 0 {
			problems = append(problems, fmt.Sprintf("%s: memories not wiped (%s)",
				name, strings.Join(row.Memories, ", ")))
		}
		var missingEnv []string
		have := map[string]bool{}
		for _, k := range row.EnvKeys {
			have[k] = true
		}
		for _, k := range EnvKeys {
			if !have[k] {
				missingEnv = append(missingEnv, k)
			}
		}
		if len(missingEnv) > 0 {
			problems = append(problems, fmt.Sprintf("%s: .env missing %s", name, strings.Join(missingEnv, ", ")))
		}
		if row.Model == "" {
			problems = append(problems, name+": config.yaml has no model.default")
		}
		if name == Real && row.TerminalCwd != m.MasterclassRoot {
			problems = append(problems, fmt.Sprintf("%s: terminal.cwd is %s, expected %s — run ensure",
				name, orUnset(row.TerminalCwd), m.MasterclassRoot))
		}
		if name != Real && row.TerminalCwd != "" {
			problems = append(problems, fmt.Sprintf("%s: eval sandbox must not pin a working directory (terminal.cwd=%s)",
				name, row.TerminalCwd))
		}
	}
	var with, without *Status
	for i := range rows {
		switch rows[i].Profile {
		case With:
			with = &rows[i]
		case Without:
			without = &rows[i]
		}
	}
	if with != nil && without != nil && !sameSHA(with.SoulSHA, without.SoulSHA) {
		problems = append(problems, "arms differ: SOUL.md of "+With+" and "+Without+" is not identical")
	}
	return rows, problems
}

func sameSHA(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Named resolves a list of profile names, defaulting to all four.
func (m *Manager) Named(args []string) ([]string, error) {
	if len(args) == 0 {
		return []string{Real, With, Without, Trigger, Revise}, nil
	}
	for _, a := range args {
		if a != Real && a != With && a != Without && a != Trigger && a != Revise {
			return nil, fmt.Errorf("unknown profile(s): %s (known: %s, %s, %s, %s, %s)",
				a, Real, With, Without, Trigger, Revise)
		}
	}
	return args, nil
}
