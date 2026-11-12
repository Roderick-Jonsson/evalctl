package runner

// Before a run spends money it says four things out loud: which hermes it drives, which profile's
// SOUL.md is the contract and why that profile was chosen, what the eval sandboxes inherit to sign
// in, and whether the launched agent produced anything the harness can read.
//
// All four were silent before, and every silence had the same failure mode — an iteration of zeros
// that looked like a bad skill. A measurement whose inputs are guesses is not a measurement, so the
// run refuses instead of scoring.

import (
	"fmt"
	"strings"

	"evalctl/internal/profile"
	"evalctl/internal/version"
)

// Preflight is the gate both lanes pass before they create a sandbox. A dry run prints the same
// facts but refuses nothing: its whole job is to show what a real run would do, including what it
// would object to.
func (r *Runner) Preflight(o Options) error {
	path, err := r.Profiles.Probe()
	if err != nil {
		if o.DryRun {
			r.message("! %v", err)
			r.announceContract()
			return nil
		}
		return err
	}
	hv := r.Profiles.HermesVersion()
	if hv == "" {
		hv = "version not reported"
	}
	if len(hv) > 80 {
		hv = hv[:80] + "…"
	}
	r.message("hermes %s · evalctl %s · %s", hv, version.Resolve().Short(), path)
	r.announceContract()
	if prob := r.Profiles.CredentialProblem(); prob != nil {
		if o.DryRun {
			r.message("! %v", prob)
			return nil
		}
		return prob
	}
	return nil
}

// announceContract says which profile's rules the run is graded against, and — when nobody named one
// — that it is a guess. The fallback is the one case where the contract can be somebody's everyday
// assistant instead of the project's, and that is worth one line before the first token is spent.
func (r *Runner) announceContract() {
	creds := r.Profiles.Credentials()
	line := fmt.Sprintf("contract: SOUL.md of profile %s (%s) · arms sign in with %s",
		r.Profiles.Real, r.Profiles.RealSource, creds.Describe())
	if r.Profiles.RealSource == profile.RealFromDefault {
		r.message("%s", line)
		r.message("  ! nothing named a profile, so this measures Hermes's default profile's rules — "+
			"set \"profile\" in %s, or run with EVALCTL_PROFILE=<name>", "evalctl.json")
		return
	}
	r.message("%s", line)
}

// StreamProblem is one finished launch, read honestly. An arm that emitted no tool call and no
// result frame produced nothing to score, and an arm whose stderr names a credential failure did not
// fail at the skill. Either way the answer is an error, never a zero.
func StreamProblem(events []map[string]any, result map[string]any, stderr string) error {
	if err := profile.SignInProblem(stderr); err != nil {
		return err
	}
	if result != nil {
		return nil
	}
	if HasEvidence(events) {
		// Tool calls but no result frame: the run was cut short (budget, early stop). That is a
		// recorded outcome, not a broken harness.
		return nil
	}
	excerpt := strings.TrimSpace(stderr)
	if excerpt == "" {
		excerpt = "stderr was empty"
	}
	if len(excerpt) > 600 {
		excerpt = excerpt[:600] + "…"
	}
	return fmt.Errorf("the launch produced no stream this harness can read (no tool call, no result frame) — "+
		"check that `hermes chat --format stream-json` works here, and that the profile is signed in\n  %s",
		indentLines(excerpt, "  "))
}

// indentLines keeps a multi-line stderr readable under a one-line refusal.
func indentLines(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}
