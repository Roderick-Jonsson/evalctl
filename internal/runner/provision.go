package runner

// The sandboxes are born for the operation that spends in them and removed when it ends.
//
// An eval profile is a clean room, and a clean room that survives its run is no longer clean: the
// next measurement inherits the last one's caches, its context-length table, its session history
// and its skill copies. It also leaks — every surface that enumerates profiles (the desktop and
// kanban assignee pickers, the decomposer's routing roster, the gateway's served set) reads the
// profile directory, so four sandboxes that are only needed for an hour are four permanent
// co-tenants of the real profile.
//
// So: provision before the first spend, remove after the last one, and `--keep` when a person
// wants to look inside the room afterwards. `evalctl profiles reap` is the recovery path for a
// run that was killed before its own teardown ran.
//
// Teardown goes through `hermes profile delete`, never os.RemoveAll: only that path stops the
// profile's backends, tombstones the directory and purges its session identity, and a hand-wiped
// directory is one `profile create` refuses to replace.

import (
	"sort"
)

// provisionSandbox creates one sandbox when it is not live and brings it to the state a run
// expects: the contract in SOUL.md, the credential keys, no memories. Skills are the caller's
// business — the with-skill arm is re-synced per case, the trigger arm gets the whole pipeline.
func (r *Runner) provisionSandbox(name string) ([]string, error) {
	var lines []string
	line, err := r.Profiles.Create(name, false)
	if err != nil {
		return lines, err
	}
	lines = append(lines, line)
	for _, step := range []func(string, bool) (string, error){
		r.Profiles.WriteEnv, r.Profiles.CopyAuth, r.Profiles.Render, r.Profiles.Scrub,
	} {
		line, err := step(name, false)
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}
	// A sandbox whose intended set is empty is emptied: `profile create --no-skills` still leaves
	// Hermes's own essential bundle behind once the profile runs, and a baseline arm that carries a
	// skill is not a baseline. A sandbox that installs skills is synced by its own caller — the
	// trigger lane's whole pipeline, the with-skill arm's per-case skill — so a plan with contents
	// is left to it.
	if len(r.Profiles.Plan(name)) == 0 {
		line, err := r.Profiles.SyncSkills(name, nil, false)
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// provisionSandboxes provisions each name and reports what it did, in pipeline order.
func (r *Runner) provisionSandboxes(names []string) error {
	for _, name := range sortedNames(names) {
		lines, err := r.provisionSandbox(name)
		for _, line := range lines {
			r.message("%s", line)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// releaseSandboxes removes the sandboxes one operation used, unless the operator asked to keep
// them. A teardown failure is reported and never fatal: the run's outputs are already on disk,
// and turning a finished measurement into a failed one would be a lie about what was measured.
func (r *Runner) releaseSandboxes(names []string, keep bool) {
	for _, name := range sortedNames(names) {
		if keep {
			r.message("%s kept — `evalctl profiles reap` removes it", name)
			continue
		}
		line, err := r.Profiles.Remove(name, false)
		if err != nil {
			r.message("teardown %s: %v", name, err)
			continue
		}
		r.message("%s", line)
	}
}

// sortedNames de-duplicates and orders a set of sandbox names, so two operations that provision
// the same room print the same lines in the same order.
func sortedNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// armSandboxes is the set of sandboxes a run with these arms needs.
func (r *Runner) armSandboxes(arms []string) []string {
	names := make([]string, 0, len(arms))
	for _, arm := range arms {
		names = append(names, r.ArmProfile(arm))
	}
	return sortedNames(names)
}
