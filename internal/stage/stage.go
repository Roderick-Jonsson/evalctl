// Package stage carries the facts about the four stage skills that belong to neither the
// contract (rule text) nor a profile (provisioning): their pipeline order, and the artifacts
// each one writes into a workspace.
package stage

import (
	"os"
	"path"
	"path/filepath"
	"sort"
)

// Order is the pipeline the contract defines. Skills are listed in it, not alphabetically,
// because "the skill under test" (and the with-arm's steady state) means the first stage —
// sorting by name would make av-production the default arm.
var Order = []string{"source-research", "learning-path", "slide-builder", "av-production"}

// Outputs is what each stage writes inside an MC-[topic]/ workspace, as glob patterns over the
// entry's base name. Every document is <PREFIX>-<topic>.md (contract, Artifact descriptors), so
// the pattern — not a fixed name — is what identifies a stage's own output. A promotion into a
// stage leaves that stage's own outputs behind: the fixture must hold the case's inputs, never
// its answers, or the run grades a no-op. A stage that grows a new output has to be listed here
// too, which is why this table is data and not a rule buried in the copier.
var Outputs = map[string][]string{
	"source-research": {"PS-*.md", "sources"},
	"learning-path":   {"LP-*.md", "PO-RevA-*.md", "PO-RevB-*.md"},
	"slide-builder":   {"SL-*.md", "pages"},
	"av-production":   {"AV"},
}

// Denied is the pattern list a promotion into skill must not carry forward. An unknown skill
// denies nothing: the promoted fixture then holds whatever the source run produced, and the
// caller is told so.
func Denied(skill string) []string { return Outputs[skill] }

// Own reports whether a workspace entry named base is one of skill's own outputs. Matching is a
// glob on the base name, so a topic-suffixed PS-writing-agent-skills.md is recognised as
// source-research's output while a fixture's hand-written PS.md is not.
func Own(skill, base string) bool {
	for _, pattern := range Outputs[skill] {
		if ok, err := path.Match(pattern, base); err == nil && ok {
			return true
		}
	}
	return false
}

// Present lists the workspace entries that match skill's own-output patterns, sorted. It is how a
// caller that needs real paths (a reference promotion takes the stage's outputs by name) turns
// the patterns into the names this workspace actually holds; a missing dir yields nothing.
func Present(ws, skill string) []string {
	entries, err := os.ReadDir(ws)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if Own(skill, e.Name()) {
			out = append(out, filepath.ToSlash(e.Name()))
		}
	}
	sort.Strings(out)
	return out
}
