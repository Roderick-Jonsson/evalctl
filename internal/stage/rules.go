// Package stage carries a tree's own conventions — the part that belongs to the project rather
// than to the harness. Everything here is data: the harness names no skill, no document and no
// folder, and a tree that says nothing gets the neutral defaults (alphabetical skills, whole
// output tree, any layout, the profile's own credentials).
package stage

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is where a tree declares its conventions, at its root. It is optional: evalctl is a
// general eval runner, and only a tree with a vocabulary of its own (stages that hand work to each
// other in documents) needs to say so.
const FileName = "evalctl.json"

// Rules is a tree's own conventions.
//
// They exist because a harness that hard-codes one project's words is a harness nobody else can
// use. Each field is empty-able, and empty always means "no convention":
//
//   - Order: the order skills are listed in. Empty = alphabetical.
//   - Outputs: for each skill, the globs its own output matches inside a run's workspace. Used by a
//     promotion, which must not carry the target's answers into its fixture. Empty = nothing is
//     recognised as an answer, so everything is carried and the caller is told so.
//   - Containment: the top-level prefixes a run may write into. Empty = anything inside the run
//     root, which is the only rule the harness itself can honestly enforce.
//   - Workspaces: the top-level dir prefixes a promotion carries as the run's workspace. Empty =
//     the whole produced tree.
//   - Env: the credential keys an arm's .env gets. Empty = whatever the real profile's .env holds.
//   - Profile: the Hermes profile this tree is measured in. Empty = whichever profile you are in,
//     or Hermes's default. It is how a tree started from outside its profile still names it.
type Rules struct {
	Note        string              `json:"note,omitempty"`
	Profile     string              `json:"profile,omitempty"`
	Order       []string            `json:"skill_order,omitempty"`
	Outputs     map[string][]string `json:"skill_outputs,omitempty"`
	Containment []string            `json:"containment_prefixes,omitempty"`
	Workspaces  []string            `json:"workspace_prefixes,omitempty"`
	Env         []string            `json:"env_keys,omitempty"`
}

// Load reads a tree's evalctl.json. A missing file is not an error: it is a tree with no
// conventions of its own, which is the general case.
func Load(root string) Rules {
	var r Rules
	b, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		return Rules{}
	}
	if err := json.Unmarshal(b, &r); err != nil {
		// A broken conventions file is worse than none: every rule below would silently be "no
		// convention", which reads as a harness that ignores the project. Say so, once.
		return Rules{Note: "evalctl.json is not valid JSON — " + err.Error()}
	}
	return r
}

// Declared reports whether the tree said anything at all. A tree that declared nothing has no
// vocabulary for `promote` to respect, and the CLI warns instead of guessing.
func (r Rules) Declared() bool {
	return len(r.Order)+len(r.Outputs)+len(r.Containment)+len(r.Workspaces)+len(r.Env) > 0 || r.Profile != ""
}

// Rank orders skills the way the tree declares, unknown names last and alphabetical. With no order
// declared everything is sorted by name, because a harness that invents an order has invented a
// project.
func (r Rules) Rank(skills []string) []string {
	rank := map[string]int{}
	for i, name := range r.Order {
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

// Denied is the pattern list a promotion into skill must not carry forward. An unknown skill
// denies nothing: the promoted fixture then holds whatever the source run produced.
func (r Rules) Denied(skill string) []string { return r.Outputs[skill] }

// Own reports whether a workspace entry named base is one of skill's own outputs. Matching is a
// glob on the base name, so a topic-suffixed artifact is recognised while a fixture's hand-written
// file is not.
func (r Rules) Own(skill, base string) bool {
	for _, pattern := range r.Outputs[skill] {
		if ok, err := path.Match(pattern, base); err == nil && ok {
			return true
		}
	}
	return false
}

// Present lists the workspace entries that match skill's own-output patterns, sorted. It is how a
// caller that needs real paths (a reference promotion takes the outputs by name) turns the patterns
// into the names this workspace actually holds; a missing dir yields nothing.
func (r Rules) Present(ws, skill string) []string {
	entries, err := os.ReadDir(ws)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if r.Own(skill, e.Name()) {
			out = append(out, filepath.ToSlash(e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// Allows reports whether a path, relative to the run root, is inside what the tree lets a run write.
// With no prefixes declared everything is allowed — the run root is the boundary the harness keeps
// itself, and it does not need the project's help to keep it.
func (r Rules) Allows(rel string) bool {
	if len(r.Containment) == 0 {
		return true
	}
	for _, prefix := range r.Containment {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// Carries reports whether a top-level entry of a run's produced tree belongs to the workspace a
// promotion stages. With no prefixes declared the whole tree is the workspace.
func (r Rules) Carries(name string) bool {
	if len(r.Workspaces) == 0 {
		return true
	}
	for _, prefix := range r.Workspaces {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
