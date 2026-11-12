// Package profile provisions the Hermes profiles the eval sandboxes run in.
//
// One contract, five profiles: the real one and four sandboxes (with-skill, baseline, trigger,
// revision). The contract is the real profile's own SOUL.md — hand-written, read and never
// written back — and every sandbox gets a byte-identical copy of it, which is what makes the arms
// a comparison of skills rather than of rules.
//
// Nothing here knows a project. The real profile is resolved ($EVALCTL_PROFILE, the profile you
// are in, the tree's own declaration, Hermes's default) and the sandbox names are derived from it,
// so `cooking` gets eval-cooking-* and a course project gets its own; two trees cannot share one.
//
// The four sandboxes are created and removed around the operation that spends in them: a clean
// room that survives its run is not a clean room. `profiles ensure` is how a person provisions
// them on purpose, and `profiles reap` removes what a crash left behind — including the names an
// earlier build used.
//
// A profile's working directory is its own fact: the real profile's terminal.cwd, when it has one,
// is what a run guards as the production root, and no sandbox may pin one (the harness's --in
// decides where a sandbox works).
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
	// The four sandboxes an operation spends in, as identities. Their profile names are derived from
	// the profile the tree is measured in (see SandboxName), so no project's name is compiled in and
	// two trees can never share a sandbox.
	ArmWith    = "with"
	ArmWithout = "without"
	ArmTrigger = "trigger"
	// ArmRevise writes the proposal the loop's step 1 asks for: an agent that reads an iteration's
	// failures and feedback and answers with a new SKILL.md. It is a sandbox of its own because the
	// arms' skills would let it read the evals it is being measured against, and the judge's framing
	// would have it grading instead of writing.
	ArmRevise = "revise"

	// DefaultReal is the profile a tree is measured in when nothing names one: Hermes's own default
	// profile. It is a fallback, never an assumption about the project.
	DefaultReal = "default"

	// profileNameLimit mirrors Hermes's PROFILE_ID_RE: ^[a-z0-9][a-z0-9_-]{0,63}$.
	profileNameLimit = 64
)

// Arms are the sandbox identities, in the order the tool reports them.
var Arms = []string{ArmWith, ArmWithout, ArmTrigger, ArmRevise}

// SandboxShape reports whether a profile name looks like an eval sandbox: eval-<anything>-<arm>.
// It is how `reap` finds what an earlier build — or another tree — left on this machine without
// having to know any project's name: an earlier build named its sandboxes eval-<project>-<arm>, and
// the shape is the only part that was ever the same.
func SandboxShape(name string) bool {
	if !strings.HasPrefix(name, "eval-") {
		return false
	}
	for _, arm := range Arms {
		if strings.HasSuffix(name, "-"+arm) && len(name) > len("eval-")+len(arm)+1 {
			return true
		}
	}
	return false
}

// StaleSandboxes lists the sandbox-shaped profiles on disk that this tree does not own: leftovers
// from an earlier build, or another project's. `reap` names them before removing them, and only
// touches one nothing is holding.
func (m *Manager) StaleSandboxes() []string {
	entries, err := os.ReadDir(filepath.Join(m.Hermes, "profiles"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !SandboxShape(name) || m.IsSandbox(name) || !m.Live(name) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// identityMarkers mirrors hermes_constants._PROFILE_IDENTITY_MARKERS: a directory under profiles/
// is a profile only when one of them is a file. Anything else is a runtime leftover.
var identityMarkers = []string{"config.yaml", ".env", "SOUL.md", "profile.yaml", "auth.json", "state.db"}

// SkillParts are the parts of a skill that get installed. evals/, .git and .DS_Store stay behind —
// the installed skill must run, not test.
var SkillParts = []string{"SKILL.md", "scripts", "references", "assets", "templates"}

// CwdSentinels are values Hermes treats as "terminal.cwd not configured".
var CwdSentinels = map[string]bool{"": true, ".": true, "./": true, "auto": true, "cwd": true}

// Manager owns the provisioning rules for one tree.
type Manager struct {
	Root       string            // the eval tree: the skills and their workspaces
	Hermes     string            // ~/.hermes, or $HERMES_ROOT
	Skills     []string          // the tree's skills, in the tree's own order
	SkillsRoot string            // where the tree's skills live: the tree's own root
	Bin        string            // the hermes executable
	Rules      stage.Rules       // the tree's conventions (empty = none declared)
	Real       string            // the profile whose SOUL.md is the contract
	RealSource string            // why that profile: $EVALCTL_PROFILE, the cwd, evalctl.json, or a fallback
	Arms       map[string]string // arm identity → the sandbox profile it runs in
	Production string            // the real profile's terminal.cwd ("" = not pinned)

	dirs map[string]string // skill → its directory on disk, found lazily (a tree may group them)
}

// New builds a manager for root. `skills` are the skills the tree declares; `rules` are its own
// conventions, which decide their order and nothing else here.
func New(root string, skills []string, rules stage.Rules) *Manager {
	hermes := os.Getenv("HERMES_ROOT")
	if hermes == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			hermes = filepath.Join(home, ".hermes")
		}
	}
	bin := os.Getenv("HERMES_BIN")
	if bin == "" {
		bin = "hermes"
	}
	// The tree is a project folder, so its skills sit under its own root (possibly grouped in
	// folders — findSkillDir walks for them). A profile's skills/ is Hermes's install directory,
	// never a source of files to copy.
	m := &Manager{Root: root, Hermes: hermes, Skills: rules.Rank(skills), SkillsRoot: root,
		Bin: bin, Rules: rules, Arms: map[string]string{}}
	m.Real, m.RealSource = RealProfileFrom(hermes, root, rules)
	for _, arm := range Arms {
		m.Arms[arm] = SandboxName(m.Real, arm)
	}
	// The production root is a fact about the profile, not a constant about the project: it is
	// whatever that profile's working directory is, and an unpinned profile simply has none.
	m.Production = m.TerminalCwd(m.Real)
	return m
}

// SandboxName is the profile one arm runs in: eval-<real>-<arm>. It is derived, never configured,
// so it cannot drift from the profile being measured, and two projects measuring different profiles
// never collide.
//
// Hermes caps a profile id at 64 characters, so a long real name gives way: the tail becomes a short
// hash of the full name, which keeps two long names from collapsing onto one sandbox.
func SandboxName(real, arm string) string {
	name := "eval-" + sanitize(real) + "-" + arm
	if len(name) <= profileNameLimit {
		return name
	}
	sum := sha256.Sum256([]byte(real))
	digest := hex.EncodeToString(sum[:])[:6]
	keep := profileNameLimit - len("eval-") - len("-"+digest+"-"+arm)
	if keep < 1 {
		keep = 1
	}
	return "eval-" + sanitize(real)[:keep] + "-" + digest + "-" + arm
}

// sanitize makes a profile name Hermes will accept: lowercase, letters, digits, hyphen, underscore.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		out = "profile"
	}
	return out
}

// Name is the profile an arm runs in.
func (m *Manager) Name(arm string) string { return m.Arms[arm] }

// Arm is the identity behind a sandbox's profile name.
func (m *Manager) Arm(name string) (string, bool) {
	for arm, n := range m.Arms {
		if n == name {
			return arm, true
		}
	}
	return "", false
}

// Sandboxes are the four profile names an operation may create, in arm order.
func (m *Manager) Sandboxes() []string {
	out := make([]string, 0, len(Arms))
	for _, arm := range Arms {
		out = append(out, m.Arms[arm])
	}
	return out
}

// RealProfile is the profile whose SOUL.md is the contract: $EVALCTL_PROFILE, else the profile this
// process is standing in, else the profile the tree declares in its evalctl.json, else the profile
// the tree *is*, else Hermes's default.
//
// Being inside a profile is the normal way in — `cd ~/.hermes/profiles/<name> && evalctl` must
// measure that profile, not a name compiled into the binary — and a tree started from elsewhere
// says which profile it belongs to. No project's name is compiled in.
func RealProfile(hermes, root string, rules stage.Rules) string {
	name, _ := RealProfileFrom(hermes, root, rules)
	return name
}

// The sources RealProfileFrom can report. They exist so a run can say *why* it chose a profile: a
// measurement against the wrong contract is money spent on the wrong question, and the one case that
// needs saying out loud is the fallback, where nobody named a profile at all.
const (
	RealFromEnv     = "$EVALCTL_PROFILE"
	RealFromCwd     = "the profile you are standing in"
	RealFromRules   = "evalctl.json"
	RealFromTree    = "the tree's own directory"
	RealFromDefault = "Hermes's default (nothing named a profile)"
)

// RealProfileFrom resolves the profile and names the evidence it resolved it from.
func RealProfileFrom(hermes, root string, rules stage.Rules) (name, source string) {
	if v := strings.TrimSpace(os.Getenv("EVALCTL_PROFILE")); v != "" {
		return v, RealFromEnv
	}
	if wd, err := os.Getwd(); err == nil {
		if at := ProfileAt(wd, hermes); at != "" {
			return at, RealFromCwd
		}
	}
	if v := strings.TrimSpace(rules.Profile); v != "" {
		return v, RealFromRules
	}
	if at := ProfileAt(root, hermes); at != "" {
		return at, RealFromTree
	}
	return DefaultReal, RealFromDefault
}

// resolved is the path with its symlinks followed, or the path itself when it does not exist.
func resolved(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return path
}

// ProfileAt names the profile a directory belongs to ("" when it is not inside one).
func ProfileAt(dir, hermes string) string {
	// /var and /private/var are the same directory on macOS, and a temp dir comes back spelled both
	// ways depending on who asks. Compare the resolved paths or every Rel below starts with "..".
	base := resolved(filepath.Join(hermes, "profiles"))
	rel, err := filepath.Rel(base, resolved(dir))
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	name := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	if name == "" || strings.HasPrefix(name, ".") {
		return ""
	}
	if info, err := os.Stat(filepath.Join(base, name)); err != nil || !info.IsDir() {
		return ""
	}
	return name
}

// LockPath is where a run takes its exclusive lock: beside the profile it measures, so two trees
// measuring one profile collide instead of writing one sandbox twice. It is not inside the profile
// directory, which `hermes profile create` owns and `profile delete` removes.
func (m *Manager) LockPath() string { return m.LockPathFor(m.Real) }

// LockPathFor is the same lock for any profile name, so a reap can ask whether somebody is holding
// a sandbox that is not this tree's before it removes it.
func (m *Manager) LockPathFor(name string) string {
	return filepath.Join(m.Hermes, ".evalctl-locks", name+".lock")
}

// Home is the profile dir: the Hermes root itself for "default", a subdir otherwise.
func (m *Manager) Home(name string) string {
	if name == "default" {
		return m.Hermes
	}
	return filepath.Join(m.Hermes, "profiles", name)
}

func (m *Manager) Soul(name string) string   { return filepath.Join(m.Home(name), "SOUL.md") }
func (m *Manager) SkillsDir(n string) string { return filepath.Join(m.Home(n), "skills") }

// Contract reads the contract — the real profile's SOUL.md — and its hash. It is the one
// hand-written input of a measurement, recorded in every iteration's meta.json.
func (m *Manager) Contract() (string, string, error) {
	body, err := m.SoulText()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(body))
	return body, hex.EncodeToString(sum[:]), nil
}

// Plan is the steady state of each profile. The with-skill arm is re-synced per case, so its entry
// is one skill rather than a fixed contents list; the trigger arm needs every skill, because one
// skill installed cannot measure misselection.
func (m *Manager) Plan(name string) []string {
	if name == m.Real {
		return m.Skills
	}
	switch arm, _ := m.Arm(name); arm {
	case ArmTrigger:
		return m.Skills
	case ArmWith:
		if len(m.Skills) > 0 {
			return m.Skills[:1]
		}
	}
	return nil
}

// Description is the one-liner Hermes shows when picking a profile. It names the arm and nothing
// about the project: what the tree is for is the tree's business.
func (m *Manager) Description(name string) string {
	arm, ok := m.Arm(name)
	if !ok {
		return ""
	}
	switch arm {
	case ArmWith:
		return "Eval sandbox — with-skill arm. Not for real work; provisioned by evalctl."
	case ArmWithout:
		return "Eval sandbox — baseline arm, no skills. Not for real work; provisioned by evalctl."
	case ArmTrigger:
		return "Eval sandbox — trigger arm, every skill installed. Not for real work; provisioned by evalctl."
	case ArmRevise:
		return "Eval sandbox — writes a skill revision from one iteration's failures. Not for real work."
	}
	return ""
}

// ---------------------------------------------------------------- SOUL.md

// SoulText is the contract itself: the real profile's SOUL.md, hand-written and never rendered.
// It reaches every session regardless of cwd, so an edit there *is* a contract change — which is
// why evalctl only ever reads this file.
func (m *Manager) SoulText() (string, error) {
	path := m.Soul(m.Real)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("contract: %s: %w (the contract is that profile's SOUL.md)", path, err)
	}
	text := string(b)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("contract: %s is empty — the contract lives there", path)
	}
	return text, nil
}

// Render copies the contract into one sandbox's SOUL.md, byte for byte. The arms differ in the
// skill they install, never in the rules they read, and one hash over both files is what proves it.
func (m *Manager) Render(name string, dry bool) (string, error) {
	text, err := m.SoulText()
	if err != nil {
		return "", err
	}
	if name == m.Real {
		return fmt.Sprintf("soul %s: the contract itself — left alone", name), nil
	}
	path := m.Soul(name)
	if b, err := os.ReadFile(path); err == nil && string(b) == text {
		return fmt.Sprintf("soul %s: already identical to %s/SOUL.md (%d bytes)", name, m.Real, len(text)), nil
	}
	msg := fmt.Sprintf("soul %s ← %s/SOUL.md (%d bytes)", name, m.Real, len(text))
	if dry {
		return msg, nil
	}
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

// SourceEnv is the real profile's .env — the credentials an arm inherits. An arm runs the same
// model in the same account: it differs by the skill it installs, not by who it is.
func (m *Manager) SourceEnv() map[string]string {
	b, err := os.ReadFile(filepath.Join(m.Home(m.Real), ".env"))
	if err != nil {
		return map[string]string{}
	}
	return parseEnvKeys(b)
}

// EnvKeys is what an arm's .env gets: the tree's own list when it declares one, else every key the
// real profile carries. Nothing about a project's credentials is compiled in.
func (m *Manager) EnvKeys() []string {
	if len(m.Rules.Env) > 0 {
		return append([]string(nil), m.Rules.Env...)
	}
	source := m.SourceEnv()
	keys := make([]string, 0, len(source))
	for k := range source {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	keys := m.EnvKeys()
	source := m.SourceEnv()
	var missing []string
	for _, k := range keys {
		if _, ok := source[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		// A declared key that is not there is a configuration change half-applied: the arm would
		// launch without the credential the tree says it needs. Refuse now, not at the first 401.
		return "", fmt.Errorf("env %s: the tree declares %s but %s does not hold %s — add them, or drop "+
			"them from env_keys in %s", name, strings.Join(missing, ", "),
			filepath.Join(m.Home(m.Real), ".env"), strings.Join(missing, ", "), stage.FileName)
	}
	have := map[string]bool{}
	for _, k := range m.EnvKeysIn(name) {
		have[k] = true
	}
	complete := true
	for _, k := range keys {
		if !have[k] {
			complete = false
		}
	}
	if complete {
		return fmt.Sprintf("env %s: already has %s", name, strings.Join(keys, ", ")), nil
	}
	msg := fmt.Sprintf("env %s/.env ← %s", name, strings.Join(keys, ", "))
	body := []string{"# Provisioned by evalctl — credentials only, settings belong in config.yaml.", ""}
	for _, k := range keys {
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

// skillDir finds one of the tree's skills on disk. A checkout may group its skills in folders or
// ship them inside a plugin bundle, so the name is resolved by walking rather than by joining.
func (m *Manager) skillDir(skill string) string {
	if d, ok := m.dirs[skill]; ok && d != "" {
		return d
	}
	if m.dirs == nil {
		m.dirs = map[string]string{}
	}
	d := findSkillDir(m.SkillsRoot, skill)
	m.dirs[skill] = d
	return d
}

// findSkillDir is the bounded walk behind skillDir: at most four levels, never into a skill's own
// contents, first match wins. It returns "" when the skill is not on disk.
func findSkillDir(root, name string) string {
	const maxDepth = 4
	var found string
	var walk func(dir string, depth int) bool
	walk = func(dir string, depth int) bool {
		if filepath.Base(dir) == name && isFileAt(filepath.Join(dir, "SKILL.md")) {
			found = dir
			return true
		}
		if depth >= maxDepth {
			return false
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "evals" ||
				e.Name() == "scripts" || e.Name() == "references" || e.Name() == "assets" ||
				e.Name() == "templates" || e.Name() == "node_modules" || strings.HasSuffix(e.Name(), "-workspace") {
				continue
			}
			if walk(filepath.Join(dir, e.Name()), depth+1) {
				return true
			}
		}
		return false
	}
	walk(root, 0)
	return found
}

func isFileAt(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// TreeHash fingerprints the parts that actually get installed — the unit a run sees.
func (m *Manager) TreeHash(skill string) (string, error) {
	digest := sha256.New()
	root := m.skillDir(skill)
	if root == "" {
		return "", fmt.Errorf("%s: %q is not on disk under %s", skill, skill, m.SkillsRoot)
	}
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
	src := m.skillDir(skill)
	if src == "" {
		return fmt.Errorf("%s: %q is not on disk under %s", skill, skill, m.SkillsRoot)
	}
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
	// Only a sandbox is synced to exactly one set. The real profile also carries whatever Hermes
	// installs for itself, and evicting that because evalctl did not ask for it is not a sync — it
	// is deleting somebody else's skill.
	exact := name != m.Real
	if dry {
		if !exact {
			return fmt.Sprintf("skills %s: ensures %s, leaves what Hermes installed alone (has %s)",
				name, orDash(wanted), orDash(current)), nil
		}
		return fmt.Sprintf("skills %s: %s → %s", name, orDash(current), orDash(wanted)), nil
	}
	dir := m.SkillsDir(name)
	if filepath.Clean(dir) == filepath.Clean(m.SkillsRoot) {
		// The tree *is* this profile's skills dir: its stage skills are already where they are read
		// from, and copying one onto itself would delete it.
		return fmt.Sprintf("skills %s: already the tree (%s)", name, orDash(wanted)), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	keep := map[string]bool{}
	for _, w := range wanted {
		keep[w] = true
	}
	for _, have := range current {
		if !keep[have] && exact {
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
	if !exact {
		return fmt.Sprintf("skills %s: ensures %s", name, orDash(wanted)), nil
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
	if !m.IsSandbox(name) {
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

func orUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

// ---------------------------------------------------------------- profiles

// Tombstone is where Hermes records a deleted profile — a marker beside the profile dirs, not
// inside one, so it survives the directory's removal.
func (m *Manager) Tombstone(name string) string {
	return filepath.Join(m.Hermes, "profiles", ".deleted", name)
}

// Live reports whether Hermes would resolve this profile: a directory carrying an identity marker
// that has not been tombstoned.
//
// Never decide this with os.Stat alone. A crashed `profile create` leaves an empty shell that
// `profile list` does not show, `-p` refuses to start, and `profile create` refuses to replace
// unless it was tombstoned first — so "the directory is there" is not the question, and answering
// it that way is how a sandbox ends up half-provisioned and unrecoverable.
func (m *Manager) Live(name string) bool {
	home := m.Home(name)
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Lstat(m.Tombstone(name)); err == nil {
		return false
	}
	for _, marker := range identityMarkers {
		if info, err := os.Stat(filepath.Join(home, marker)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// Create makes the profile with no skills and no alias.
func (m *Manager) Create(name string, dry bool) (string, error) {
	if m.Live(name) {
		return fmt.Sprintf("create %s: exists", name), nil
	}
	if dry {
		return fmt.Sprintf("create %s (hermes profile create --no-skills --no-alias)", name), nil
	}
	desc := m.Description(name)
	out, err := exec.Command(m.Bin, "profile", "create", name, "--no-skills", "--no-alias",
		"--description", desc).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes profile create %s failed:\n%s", name, out)
	}
	if !m.Live(name) {
		return "", fmt.Errorf("hermes profile create %s reported success but %s is not a profile", name, m.Home(name))
	}
	return "create " + name, nil
}

// Remove deletes one sandbox through `hermes profile delete`, which is the only path that stops
// the profile's backends, tombstones the directory and purges its session identity. Removing the
// directory by hand leaves a marker-less shell that `profile create` then refuses to replace, so
// this verb is the teardown, never `rm -rf`.
func (m *Manager) Remove(name string, dry bool) (string, error) {
	if name == m.Real {
		return "", fmt.Errorf("remove %s: refused — the real profile is not a sandbox", name)
	}
	if !m.IsSandbox(name) && !SandboxShape(name) {
		return "", fmt.Errorf("remove %s: refused — not an eval sandbox (one of %s)",
			name, strings.Join(m.Sandboxes(), ", "))
	}
	if _, err := os.Stat(m.Home(name)); err != nil {
		return fmt.Sprintf("remove %s: not provisioned", name), nil
	}
	if dry {
		return fmt.Sprintf("remove %s (hermes profile delete -y)", name), nil
	}
	out, err := exec.Command(m.Bin, "profile", "delete", "-y", name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes profile delete %s failed:\n%s", name, out)
	}
	if m.Live(name) {
		return "", fmt.Errorf("hermes profile delete %s reported success but %s is still a live profile", name, m.Home(name))
	}
	return "remove " + name, nil
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
			_ = exec.Command(m.Bin, "profile", "describe", name, "--text", m.Description(name)).Run()
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
	if name != m.Real {
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
	Profile      string   `json:"profile"`
	Exists       bool     `json:"exists"`
	SoulSHA      *string  `json:"soul_sha256"`
	SoulOK       bool     `json:"soul_ok"` // reads the contract it should: its own (real) or a copy of it
	Skills       []string `json:"skills"`
	SkillsOK     bool     `json:"skills_ok"`
	Memories     []string `json:"memories"`
	EnvKeys      []string `json:"env_keys"`
	AuthFiles    []string `json:"auth_files"`
	ExternalDirs []string `json:"external_dirs,omitempty"`
	Model        string   `json:"model"`
	TerminalCwd  string   `json:"terminal_cwd"`
}

// Status reports one profile's state.
func (m *Manager) Status(name string) Status {
	soul := m.SoulSHA(name)
	var contract *string
	if name != m.Real {
		contract = m.SoulSHA(m.Real)
	}
	soulOK := soul != nil && (name == m.Real || sameSHA(soul, contract))
	skills := m.InstalledSkills(name)
	skillsOK := sameSet(skills, m.Plan(name))
	switch arm, _ := m.Arm(name); {
	case arm == ArmWith:
		skillsOK = len(skills) == 1
	case name == m.Real:
		// The real profile also carries whatever Hermes installs for itself (its essential bundle),
		// so the rule there is that every skill of the tree is present, not that nothing else is.
		skillsOK = containsAll(skills, m.Plan(name))
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
	exists := m.Live(name)
	// The real profile's sign-in includes the Hermes root's shared files; a sandbox only mirrors
	// what was copied into it, so its own directory is the whole answer there.
	authFiles := authFilesIn(m.Home(name))
	if name == m.Real {
		authFiles = m.Credentials().AuthFiles
	}
	return Status{
		Profile:      name,
		Exists:       exists,
		SoulSHA:      soul,
		SoulOK:       soulOK,
		Skills:       skills,
		SkillsOK:     skillsOK,
		Memories:     memories,
		EnvKeys:      m.EnvKeysIn(name),
		AuthFiles:    authFiles,
		ExternalDirs: m.ExternalDirs(name),
		Model:        modelDefault(m.Home(name)),
		TerminalCwd:  m.TerminalCwd(name),
	}
}

// authFilesIn lists the login files one profile directory holds.
func authFilesIn(home string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && authName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// containsAll reports whether every one of want is in have, ignoring what else is there.
func containsAll(have, want []string) bool {
	present := map[string]bool{}
	for _, h := range have {
		present[h] = true
	}
	for _, w := range want {
		if !present[w] {
			return false
		}
	}
	return true
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
	for _, name := range names {
		row := m.Status(name)
		rows = append(rows, row)
		if !row.Exists {
			// A sandbox exists only for the operation that is using it, so "missing" is the state
			// after every run and not a deviation. The real profile is the one that must be there.
			if name != m.Real {
				continue
			}
			problems = append(problems, name+": profile missing")
			continue
		}
		// The contract is the real profile's SOUL.md. An arm reads a copy of it; a copy that is not
		// byte-identical is a different rule set under one name, which is the one thing the
		// comparison cannot survive.
		switch {
		case row.SoulSHA == nil && name == m.Real:
			problems = append(problems, name+": SOUL.md is missing — the contract lives there")
		case row.SoulSHA == nil:
			problems = append(problems, name+": SOUL.md is missing — run render")
		case !row.SoulOK:
			problems = append(problems, fmt.Sprintf("%s: SOUL.md differs from %s's — run render", name, m.Real))
		}
		// The real profile is the person's own working profile: evalctl only ever adds skills there,
		// never removes them, so a skill the tree declares and the profile does not carry is not a
		// drift to report between runs. A sandbox is synced exactly, so there it is.
		if name != m.Real && !row.SkillsOK {
			problems = append(problems, fmt.Sprintf("%s: skills %v ≠ %v", name, row.Skills, m.Plan(name)))
		}
		if name != m.Real && len(row.Memories) > 0 {
			problems = append(problems, fmt.Sprintf("%s: memories not wiped (%s)",
				name, strings.Join(row.Memories, ", ")))
		}
		var missingEnv []string
		have := map[string]bool{}
		for _, k := range row.EnvKeys {
			have[k] = true
		}
		for _, k := range m.EnvKeys() {
			if !have[k] {
				missingEnv = append(missingEnv, k)
			}
		}
		if len(missingEnv) > 0 {
			problems = append(problems, fmt.Sprintf("%s: .env missing %s", name, strings.Join(missingEnv, ", ")))
		}
		if name == m.Real {
			// The real profile is where the credentials live; a sandbox only mirrors them.
			if len(row.EnvKeys) == 0 && len(row.AuthFiles) == 0 {
				problems = append(problems, name+": no credentials — the eval sandboxes will have nothing to sign in with")
			}
		} else {
			have := map[string]bool{}
			for _, f := range row.AuthFiles {
				have[f] = true
			}
			var missingAuth []string
			for _, src := range m.authSources() {
				if !have[src.Name] {
					missingAuth = append(missingAuth, src.Name)
				}
			}
			if len(missingAuth) > 0 {
				problems = append(problems, fmt.Sprintf("%s: no login file %s — run ensure",
					name, strings.Join(missingAuth, ", ")))
			}
		}
		if p := m.LinkProblem(name); p != "" {
			problems = append(problems, p)
		}
		if row.Model == "" {
			problems = append(problems, name+": config.yaml has no model.default")
		}
		if name != m.Real && row.TerminalCwd != "" {
			problems = append(problems, fmt.Sprintf("%s: eval sandbox must not pin a working directory (terminal.cwd=%s)",
				name, row.TerminalCwd))
		}
	}
	return rows, problems
}

func sameSHA(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Named resolves a list of profile names, defaulting to the real profile and its four sandboxes.
// The old sandbox names are accepted too, so a machine that ran an earlier build can be reaped.
func (m *Manager) Named(args []string) ([]string, error) {
	if len(args) == 0 {
		return append([]string{m.Real}, m.Sandboxes()...), nil
	}
	for _, a := range args {
		if !m.Known(a) {
			return nil, fmt.Errorf("unknown profile(s): %s (known: %s)",
				a, strings.Join(append([]string{m.Real}, m.Sandboxes()...), ", "))
		}
	}
	return args, nil
}

// Known reports whether name is one of this tree's profiles: the real one, a sandbox, or a sandbox
// name an earlier build left behind.
func (m *Manager) Known(name string) bool {
	if name == m.Real {
		return true
	}
	return m.IsSandbox(name) || SandboxShape(name)
}

// IsSandbox reports whether name is one of the four profiles an operation may create and remove.
func (m *Manager) IsSandbox(name string) bool {
	_, ok := m.Arm(name)
	return ok
}

// ReapTargets is every sandbox `reap` may remove: the four this tree would create, and the
// sandbox-shaped profiles nothing here owns. The real profile is never a target.
func (m *Manager) ReapTargets() []string {
	return append(m.Sandboxes(), m.StaleSandboxes()...)
}
