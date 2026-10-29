// Package stage carries the facts about the four stage skills that belong to neither the
// contract (rule text) nor a profile (provisioning): their pipeline order, and the artifacts
// each one writes into a workspace.
package stage

// Order is the pipeline the contract defines. Skills are listed in it, not alphabetically,
// because "the skill under test" (and the with-arm's steady state) means the first stage —
// sorting by name would make av-production the default arm.
var Order = []string{"source-research", "learning-path", "slide-builder", "av-production"}

// Outputs is what each stage writes inside an MC-[topic]/ workspace. A promotion into a stage
// leaves that stage's own outputs behind: the fixture must hold the case's inputs, never its
// answers, or the run grades a no-op. A stage that grows a new output has to be listed here too,
// which is why this table is data and not a rule buried in the copier.
var Outputs = map[string][]string{
	"source-research": {"PS.md", "sources"},
	"learning-path":   {"LP.md", "PO-RevA.md", "PO-RevB.md"},
	"slide-builder":   {"SL.md", "pages"},
	"av-production":   {"AV"},
}

// Denied is what a promotion into skill must not carry forward. An unknown skill denies nothing:
// the promoted fixture then holds whatever the source run produced, and the caller is told so.
func Denied(skill string) []string { return Outputs[skill] }
