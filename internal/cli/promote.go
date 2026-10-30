package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

const promoteUsage = `usage: evalctl promote --from <run>/<case>[/<arm>] --skill <skill> --slug <new-slug>
                            [--id N] [--prompt <text|->] [--expected <text|->] [--dry-run]

Turn a finished run's output into the next stage's fixture: the MC-[topic]/ workspace is copied
into <skill>/evals/files/<new-slug>/, the target stage's own outputs are left behind, and a case
carrying the provenance is appended. <run> is 2, iteration-2 or source-research@2 (the skill's
workspace the run lives in), <arm> defaults to with_skill.

       evalctl promote --to-references --from <run>/<case>[/<arm>] [--skill S]
                            [--file <path>]... [--as <name|dir/>] [--all]
                            [--workspace MC-x] [--force] [--dry-run]

Turn a run's own stage output into a worked example in the skill's references/: PS-<topic>.md + sources/
for source-research, LP-<topic>.md + the PO-Rev*-<topic>.md files for learning-path, and so on. That is the file an agent
reads to learn the shape of a run — promote it, adapt it by hand, and the next run sees it.
--skill defaults to the case's own skill; --file narrows to one path, --all takes the workspace;
--as renames (or names a dir for several files). An existing reference is never replaced without
--force, and every promotion is logged in .evalctl/references.json.

       evalctl promote --to-trigger --from <case-ref> [--skill S] [--split train|validation]
                            [--label yes|no] [--why <text|->] [--id N] [--dry-run]

Turn a case's prompt into a trigger query: same text, the stage under test, and a why naming the
case it came from. <case-ref> is o:source-research:3, source-research:3 or a bare id that only one skill uses. --skill
defaults to the case's own skill; pass a sibling with --label no to make a real negative.`

// refFiles collects --file, repeatable and comma-separated: a promotion usually takes one
// artifact but a stage's output can be a set (LP-<topic>.md and its two PO revisions).
type refFiles []string

func (r *refFiles) String() string { return strings.Join(*r, ",") }

func (r *refFiles) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*r = append(*r, part)
		}
	}
	return nil
}

// runPromote implements `evalctl promote`: the three ways a finished eval artifact becomes
// something else — a run's output into the next stage's fixture, a run's own output into the
// skill's references/, and a case's prompt into a trigger query. None of them overwrites: a
// fixture needs a new slug, a query needs text the lane lacks, a reference needs --force.
func runPromote(args []string) int {
	fs, c := newFS("promote")
	from := fs.String("from", "", "the run to promote: <run>/<case-slug>[/<arm>], or a case ref with --to-trigger")
	skill := fs.String("skill", "", "stage skill the output becomes the fixture of (required); --to-references: the skill whose references/ receives it (default: the case's own); --to-trigger: the skill under test (default: the case's own)")
	slug := fs.String("slug", "", "new case slug, also its fixture dir (output mode, required, must be new)")
	id := fs.Int("id", 0, "case id / trigger id (default: next free)")
	prompt := fs.String("prompt", "", "the case prompt (- for stdin)")
	expected := fs.String("expected", "", "expected_output (- for stdin)")
	split := fs.String("split", "", "--to-trigger: train or validation (default train)")
	label := fs.String("label", "", "--to-trigger: yes (should trigger, default) or no")
	why := fs.String("why", "", "--to-trigger: why the row exists (default: names the source case)")
	toTrigger := fs.Bool("to-trigger", false, "promote a case's prompt into a trigger query instead of a fixture")
	toRefs := fs.Bool("to-references", false, "promote the run's own stage output into the skill's references/ (a worked example)")
	as := fs.String("as", "", "--to-references: destination inside references/ (rename, or a dir for several files)")
	all := fs.Bool("all", false, "--to-references: take the whole MC-*/ workspace, not just the stage's outputs")
	workspace := fs.String("workspace", "", "--to-references: which MC-x to take when the arm wrote several")
	force := fs.Bool("force", false, "--to-references: replace an existing reference (old bytes in .evalctl/backups/)")
	dryRun := fs.Bool("dry-run", false, "print the plan, write nothing")
	var files refFiles
	fs.Var(&files, "file", "--to-references: workspace-relative path to take (repeatable, comma-separated)")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *from == "" {
		return fail(fmt.Errorf("--from is required\n\n%s", promoteUsage))
	}
	if *toTrigger {
		if *toRefs {
			return fail(fmt.Errorf("--to-trigger and --to-references are different destinations — pick one"))
		}
		return promotePrompt(c, *from, *skill, *split, *label, *why, *id, *dryRun)
	}

	src, err := resolvePromotionSource(c.s, *from)
	if err != nil {
		return fail(err)
	}
	if *toRefs {
		return promoteReferences(c, src, referenceRequest{
			Skill: *skill, As: *as, All: *all, Workspace: *workspace,
			File: files, Force: *force, DryRun: *dryRun})
	}
	if *skill == "" || *slug == "" {
		return fail(fmt.Errorf("--skill and --slug are required (or --to-trigger / --to-references)\n\n%s", promoteUsage))
	}
	cse := store.PromoteRequest{Skill: *skill, Slug: *slug, ID: *id, Source: src.Dir,
		SourceSkill: src.Skill, Run: src.Run, Case: src.Case, Arm: src.Arm, DryRun: *dryRun}
	if cse.Prompt, err = value(*prompt); err != nil {
		return fail(err)
	}
	if cse.Expected, err = value(*expected); err != nil {
		return fail(err)
	}

	// A promotion writes fixture dirs a concurrent run could be staging, so it takes the same
	// exclusive lock the runner does.
	unlock, err := lockRun(c.s.Root)
	if err != nil {
		return fail(err)
	}
	defer unlock()

	res, err := c.s.PromoteOutput(cse)
	if err != nil {
		return fail(err)
	}
	if *dryRun {
		printPromotion(res)
		fmt.Println(styleDim.Render("  dry run: nothing was written"))
		return 0
	}

	changes := []string{fmt.Sprintf("promoted %s/%s/%s into %s case #%d (%s)",
		store.RefOf(res.SourceSkill, res.Run), res.Case, res.Arm, res.Skill, res.ID, res.Slug)}
	changes = append(changes, fmt.Sprintf("  staged %d file(s) from %s", len(res.Files), res.Source))
	if len(res.Skipped) > 0 {
		changes = append(changes, "  left behind (the target stage writes these): "+strings.Join(res.Skipped, ", "))
	}
	changes = append(changes, "  fixture sha256 "+res.SHA256)
	return saveTouched(c, changes, map[string]bool{res.Skill: true})
}

// promotePrompt is the trigger half of the verb: one case's prompt becomes a query row. It shares
// the run lock with the runner for the same reason the fixture copy does — a run in flight is
// reading the set this writes.
func promotePrompt(c *common, from, skill, split, label, why string, id int, dryRun bool) int {
	if strings.Contains(from, "/") {
		return fail(fmt.Errorf("--to-trigger takes a case ref (o:source-research:3 or source-research:3), not a run path: %q", from))
	}
	ref, err := parseRef(c.s, store.LaneOutput, from)
	if err != nil {
		return fail(err)
	}
	if ref.Lane != store.LaneOutput {
		return fail(fmt.Errorf("--to-trigger promotes an output case's prompt; %q names a trigger row", from))
	}
	req := store.PromotePromptRequest{Skill: ref.Skill, Index: ref.Index, Under: skill, Split: split,
		ShouldTrigger: true, ID: id, DryRun: dryRun}
	switch label {
	case "", "yes":
	case "no":
		req.ShouldTrigger = false
	default:
		return fail(fmt.Errorf("--label must be yes or no"))
	}
	if req.Why, err = value(why); err != nil {
		return fail(err)
	}

	unlock, err := lockRun(c.s.Root)
	if err != nil {
		return fail(err)
	}
	defer unlock()

	res, err := c.s.PromotePrompt(req)
	if err != nil {
		return fail(err)
	}
	if dryRun {
		printQueryPromotion(res)
		fmt.Println(styleDim.Render("  dry run: nothing was written"))
		return 0
	}
	changes := []string{fmt.Sprintf("added trigger #%d (%s, %s, %s) from %s case #%d (%s)",
		res.ID, res.Skill, res.Split, triggerLabel(res.ShouldTrigger), res.SourceSkill, res.SourceID, res.SourceSlug)}
	changes = append(changes, fmt.Sprintf("  query  %s", clipOneLine(res.Query, 90)))
	changes = append(changes, fmt.Sprintf("  why    %s", res.Why))
	return saveTouched(c, changes, map[string]bool{store.LaneTrigger: true})
}

// referenceRequest is the --to-references half's flags, after the two shared defaults are filled
// in: the skill that owns the case, and the arm already resolved by --from.
type referenceRequest struct {
	Skill     string
	As        string
	Workspace string
	File      []string
	All       bool
	Force     bool
	DryRun    bool
}

// promoteReferences puts a run's own stage output into the skill's references/ dir. The copy
// changes the skill tree a run installs, so it takes the runner's lock like every other
// promotion, and it says out loud the one thing the tool will not do for you: the skill only
// reads a reference its SKILL.md names.
func promoteReferences(c *common, src promoSource, req referenceRequest) int {
	if req.Skill == "" {
		req.Skill = c.s.SkillOfSlug(src.Case)
	}
	if req.Skill == "" {
		return fail(fmt.Errorf("--skill is required: %q is not a case in any stage skill's evals.json", src.Case))
	}
	unlock, err := lockRun(c.s.Root)
	if err != nil {
		return fail(err)
	}
	defer unlock()

	res, err := c.s.PromoteReferences(store.ReferenceRequest{
		Skill: req.Skill, Source: src.Dir, SourceSkill: src.Skill, Run: src.Run, Case: src.Case, Arm: src.Arm,
		Workspace: req.Workspace, File: req.File, All: req.All, As: req.As,
		Force: req.Force, DryRun: req.DryRun})
	if err != nil {
		return fail(err)
	}
	if req.DryRun {
		printReference(res)
		fmt.Println(styleDim.Render("  dry run: nothing was written"))
		return 0
	}
	fmt.Println(styleChange.Render("  ✓ ") + fmt.Sprintf("promoted %s/%s/%s → %s/%s (%d file(s))",
		res.Run, res.Case, res.Arm, res.Skill, res.Workspace, len(res.Files)))
	for i, target := range res.Targets {
		fmt.Printf("      %s  ←  %s\n", target, res.Files[i])
	}
	if len(res.Overwrote) > 0 {
		fmt.Println(styleWarn.Render("  replaced: " + strings.Join(res.Overwrote, ", ") + "  (old bytes in .evalctl/backups/)"))
	}
	fmt.Println(styleDim.Render("  logged in " + relPath(c.s.Root, res.LogPath)))
	printReferenceHint(res)
	return 0
}

// relPath prints a path relative to the tree root, falling back to the absolute path.
func relPath(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}

// printReference is the dry run's read-out: what lands under references/, and what stays behind.
func printReference(res store.Reference) {
	fmt.Println(styleTitle.Render(fmt.Sprintf("would add %d file(s) to %s/references/ from %s/%s/%s",
		len(res.Files), res.Skill, res.Run, res.Case, res.Arm)))
	fmt.Printf("  workspace    %s\n", res.Workspace)
	for i, target := range res.Files {
		fmt.Printf("  %-28s ←  %s\n", res.Targets[i], target)
	}
	if len(res.Overwrote) > 0 {
		fmt.Printf("  replaces     %s (needs --force; the old bytes go to .evalctl/backups/)\n",
			strings.Join(res.Overwrote, ", "))
	}
	if len(res.Skipped) > 0 {
		fmt.Printf("  not taken    %s\n", clipList(res.Skipped, 6))
	}
}

// printReferenceHint names the line to add to SKILL.md. Nothing reads references/ on its own:
// progressive disclosure is only real when the skill says when to open the file.
func printReferenceHint(res store.Reference) {
	if len(res.Targets) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(styleWarn.Render("  next: name it in " + res.Skill + "/SKILL.md, or the agent never opens it"))
	fmt.Println(styleDim.Render(fmt.Sprintf("        - `references/%s` is a worked example of the finished shape — take its shape, never its content.",
		res.Targets[0])))
}

func triggerLabel(should bool) string {
	if should {
		return "should-trigger"
	}
	return "should-not-trigger"
}

func clipOneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// clipList shows the first few of a long list and says how many are left: a workspace listing
// that fills four lines tells you less than six names and a count.
func clipList(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s, … and %d more", strings.Join(items[:n], ", "), len(items)-n)
}

// printQueryPromotion is the dry run's read-out: the row exactly as it would land.
func printQueryPromotion(res store.QueryPromotion) {
	fmt.Println(styleTitle.Render(fmt.Sprintf("would add trigger #%d (%s, %s, %s)",
		res.ID, res.Skill, res.Split, triggerLabel(res.ShouldTrigger))))
	fmt.Printf("  from case    %s #%d (%s)\n", res.SourceSkill, res.SourceID, res.SourceSlug)
	fmt.Printf("  why          %s\n", res.Why)
	fmt.Printf("  query        %s\n", wrapIndent(res.Query, 15))
}

// promoSource is a resolved --from: one arm dir of one case of one iteration.
type promoSource struct {
	Dir   string
	Skill string // source-research — the workspace the run lives in
	Run   string // iteration-2
	Case  string // src-4-agentskills
	Arm   string // with_skill
}

// Ref names the source run unambiguously: source-research@iteration-2.
func (p promoSource) Ref() string { return store.RefOf(p.Skill, p.Run) }

// resolvePromotionSource accepts <run>/<case-slug>[/<arm>], where run is 2, iteration-2 or
// source-research@2 — the skill prefix is required once two workspaces both hold that number — and
// the arm defaults to with_skill, the arm whose artifact is worth grading.
func resolvePromotionSource(s *store.Store, ref string) (promoSource, error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(ref), "/"), "/")
	if len(parts) < 2 || len(parts) > 3 {
		return promoSource{}, fmt.Errorf("bad --from %q: want <run>/<case-slug>[/<arm>], e.g. source-research@2/src-4-agentskills/with_skill", ref)
	}
	info, err := runner.New(s).ResolveRun(parts[0], store.LaneOutput)
	if err != nil {
		return promoSource{}, err
	}
	arm := runner.ArmWith
	if len(parts) == 3 {
		arm = strings.TrimSpace(parts[2])
	}
	caseDir := filepath.Join(info.Path, parts[1])
	dir := filepath.Join(caseDir, arm)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if arms := armDirs(caseDir); len(arms) > 0 {
			return promoSource{}, fmt.Errorf("%s/%s has no arm %q (on disk: %s)", info.Ref(), parts[1], arm,
				strings.Join(arms, ", "))
		}
		return promoSource{}, fmt.Errorf("%s holds no %q case — check the case slug (evalctl runs)", info.Ref(), parts[1])
	}
	return promoSource{Dir: dir, Skill: info.Skill, Run: info.Name, Case: parts[1], Arm: arm}, nil
}

func armDirs(caseDir string) []string {
	entries, err := os.ReadDir(caseDir)
	if err != nil {
		return nil
	}
	var arms []string
	for _, e := range entries {
		if e.IsDir() {
			arms = append(arms, e.Name())
		}
	}
	sort.Strings(arms)
	return arms
}

// printPromotion is the dry run's read-out: exactly what would land in the fixture, and what is
// deliberately left behind.
func printPromotion(res store.Promotion) {
	fmt.Println(styleTitle.Render(fmt.Sprintf("would promote %s/%s/%s → %s case #%d (%s)",
		store.RefOf(res.SourceSkill, res.Run), res.Case, res.Arm, res.Skill, res.ID, res.Slug)))
	fmt.Printf("  from         %s\n", res.Source)
	fmt.Printf("  workspaces   %s\n", strings.Join(res.Workspaces, ", "))
	fmt.Printf("  fixture      %s\n", res.FixtureDir)
	fmt.Printf("  stages       %d file(s)\n", len(res.Files))
	for _, f := range res.Files {
		fmt.Println("               " + f)
	}
	if len(res.Skipped) > 0 {
		fmt.Printf("  left behind  %s\n", strings.Join(res.Skipped, ", "))
	}
}
