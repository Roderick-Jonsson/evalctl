# Run the output lane: judge what the skill produces

With the skill loaded, does the work come out right? Every case runs twice — with the skill and
without — and a judge grades each arm's assertions. Three views: the cases, the iteration, the
trend.

## The output lane

The other optimization, and the other half of the question: with the skill loaded, does the work come
out right? It is agentskills.io's *Evaluating skill output quality* loop, computed from disk. Every
case in `<skill>/evals/evals.json` runs twice — with the skill and without it — into
`<skill>-workspace/iteration-N/<slug>/with_skill|without_skill/`, and the judge grades each arm's
declared assertions PASS or FAIL with the evidence it read.

The skill chooser is the same table in this lane's terms: how many cases the skill declares, what the
last iteration scored on each arm, and how many iterations it has measured.

```
 output optimization > choose a skill                                                         evalctl v0.9.0 · 11:06
▌ SKILLS
  SKILL                CASES     WITH  WITHOUT   ITERS
▶ dummy                    2     100%     100%       1
  learning-path            2        —        —       2
```

`enter` opens the skill's own screen: the lane's three jobs as three rows, the choice on the left and
the row under the cursor said properly on the right — the same split as the door and the skill
chooser, because the three views are a choice and not a step of one run.

```
 output optimization > dummy                                                         evalctl v0.9.0 · 11:06
▌ SECTIONS                               │   TEST CASES
  ────────────────────────────────────   │   ───────────────────────────────────────────────────────
▶ test cases                             │   the set the loop is measured on
    3 cases · 6 assertions               │   A case is a prompt, the expected output, the fixtures it stages
                                         │   and the assertions a judge reads — and an assertion is a
  iteration                              │   sentence a PASS or a FAIL can be written against, which a
    iteration #2 · 1 assertion failed    │   total is not.
                                         │
  testing quality                        │   It writes
    best #2 · 2 iterations on disk       │   the set        dummy/evals/evals.json
                                         │   fixtures       dummy/evals/files/<slug>/
                                         │
                                         │   → enter  the cases · S opens evals.json, e writes the assertions
```

The cursor is the one thing the disk decides — it opens on the view that is asking for work: **test
cases** while the loop is closed, **iteration** when the newest run has failures to read, **testing
quality** when the trend is all there is to look at. `1` `2` `3` open a view **here and only here**: the
menu is the one screen where the three are a choice. Inside a view there is no rail of the three: the
view's own name is the bar path's last segment, and the row under the bar is only the loop's numbers —
`3 cases · 6 assertions · iteration #2 · best #2` — which no view counts for itself. No key changes
view; `esc` is the way back to the menu, and `esc` again is the skills.

**The loop starts closed**, and that is a state of the initialization view rather than a screen of its
own: a band above the case list draws the entry's steps as chips — `✓ cases ● run ○ assertions` and, once
a run exists, `○ grade` — with the one errand that opens the loop and the key that does it
(`→ R run iteration 1 — 2 cases, both arms · nothing is judged yet`). The order is the guide's: the
**first run comes before the first assertion**, because the assertions are written from what that run
writes. A run over a set with no assertion is not a measurement — nothing is judged, and the outputs are
the point — so it is offered, and the screen says *nothing is judged yet* before the spend. The band goes
when the first verdict lands, and comes back if a case is left unasserted later — it is the disk's
reading, not a screen the lane is stuck on.

**While the loop is closed, the other two views are shut.** The menu draws them dim with
`locked until the loop opens`, and the pane under the cursor says what opens them — there is no action
line on a row that cannot open, and `enter` or its number refuses in the status row rather than
opening a screen that would answer "nothing measured yet". The way in is the declaration and the first
look: cases, a run, an assertion on every one, the grade — the set is where the loop is opened, so the
set is the only view a closed loop offers.

**The five steps of a case, from the view that holds it.** The pane's action line is the case's own
ladder, one key at a time — **A** `S` writes the prompt and the expected output, **B** `r` runs the case
for the first time, **C** `v` reads what the arms wrote and `e` writes the assertions from it, **D** `g`
grades them into the first `grading.json`, **E** `f` writes the human review. The ladder is per case and
not per set: five cases at five different steps are five different next moves, and the pane only ever
shows the one under the cursor. `r` and `R` both run the judge with the run when the case declares an
assertion, so B and D are one spend apart only when B had nothing to judge.

**1 · test cases — declare the case.** The left column is the cases: one glyph and one number each — the
glyph is the case's own reading (`✓` green measures the skill, `○` orange proves nothing, `✗` red holds
an assertion the skill makes worse, quiet marks while nothing is graded) and the number is its
`SIGNAL` count, the assertions that measure the skill over the assertions declared. The pane beside it is
two groups, `GRADING` and `CASE`, and it shows **the case as its own record writes it** — `prompt`,
`expected_output`, `files` (the fixtures the run root is given, or `none — the run root starts empty`) —
the fields keeping the file's names and the file's order, because the pane is meant to be read as the case
and not as a summary of it.

The assertion table under `GRADING` has the two arms as columns, `WITH SKILL` and `WITHOUT SKILL`, and a
`PATTERN` column written as the move each row asks for: `adds value` (nothing to do — pass with the skill,
fail without), **`make it fail`** (the baseline passes it too: sharpen the assertion until the arm without
the skill fails it), `fix or drop` (neither arm can do it), `fix the skill` (the baseline passes it and the
skill makes it worse), `not graded`. The colours follow the meaning and not the word: in the `WITHOUT
SKILL` column a **fail is the healthy outcome** — it is the baseline failing that proves the skill does
anything — so the cell prints `fail ✓`, and a `pass` there prints `pass ✗` in the warning colour, because
the model already does it. Two green ticks used to read as "all good" when the honest reading was
"the skill changed nothing here", and a red fail in that column used to pull the eye to the assertion
that was working. Two green ticks used to read as "all good" when the honest reading was
"the skill changed nothing here", and a red fail in that column used to pull the eye to the assertion
that was working. The tag marks a case, it does not retire one: an iteration still runs it, because a
rate over a subset of the set is not comparable with a rate over the set.

The pane is **two named groups**, and the one you came for is drawn first: `GRADING` once the case has
grades, `CASE` while the loop is closed. `GRADING` runs in four registers — the arm rates, then the
case's reading in one sentence, then the assertion table, then the key to the colours — so a short
window loses the footnote before it loses the table. `CASE` is `prompt`, `expected_output` and `files`
as three labelled lines in the file's order, because a card per field turns four short facts into a
page. A group goes whole or not at all; when `CASE` cannot fit beside a complete verdict the pane
prints one line saying where those fields are (`S`), rather than half a record.

**The set is made and unmade in this view.** `n` opens the two fields a case cannot be written
without — `prompt` and `expected_output` — where the record is drawn; `ctrl+s` writes the row and
leaves the cursor on it, and `esc` throws the draft away. The id is the next free one and the slug is
the tool's (`new-case-N`, renamed in `evals.json` if it should say something else), because both are
the names the fixture directory, the iteration directory and the grades are made from. A case with no
prompt is refused where it is typed: it cannot be run, and the store refuses it too. `d` asks first,
in the same dialog every destructive step uses, and the dialog says whether a fixture directory goes
with the case — a case is its files as much as its lines.

**And it is where a case is worked on.** `r` runs the case under the cursor — both arms, then the
judge — and writes an iteration of that one case. It is a probe and never a measurement: a rate over
part of the set is not comparable with a rate over the set, so the band says *iteration 3 ran 1 of 5*
and the table never ranks it. That is the loop an assertion is written in: run the case, read what the
arms did, sharpen the sentence, run it again. `R` runs every case and is always offered, because a
fully declared set is measurable in one spend — the only thing it refuses is a set with a case nothing
can judge.

```
 output optimization > dummy > test cases                                             evalctl v0.9.0
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 2 cases · 4 assertions · iteration #1 · 1 on disk · best #1
                              │
▌ CASES                       │   dummy-2-three-dice                                         #2
  ─────────────────────────   │
    CASE               SIGNAL │   GRADING                                         1 of 2 failed
  ○ dummy-1-one-die       0/2 │   ────────────────────────────────────────────────
▶ ○ dummy-2-three-dice    1/2 │
                              │   adds value  the skill is what makes it pass — every assertion
                              │               passes with it, and not every one without it
                              │
                              │   PATTERN          ASSERTION             WITH SKILL  WITHOUT SKILL
                              │   ────────────────────────────────────────────────────────────────
                              │   make it fail   Three faces                 pass         pass ✗
                              │   adds value       The total adds up           pass         fail ✓
                              │                    ↳ without · the reply said so
                              │
                              │   CASE
                              │   ────────────────────────────────────────────────
                              │   prompt           Roll three d20.
                              │   expected_output  Three faces and a total.
                              │   files            none — the run root starts empty
```

`e` writes that list where it is drawn: the field takes the assertion block's place in the pane, one
assertion per line, the way `evalctl edit --assertions` takes them, so the record above it stays on
screen while the sentence it is about is typed. `ctrl+s` writes `<skill>/evals/evals.json` (backed up
first), `esc` discards, and enter is a line in the field rather than a submit. The save says one more
thing: the verdicts on screen were read against the *old* list, so `g` re-grades (it asks first —
the run already judged this iteration once). An assertion added
after grading shows a dash for both arms until it is judged; clearing the field leaves the case with no
`assertions` key at all, the shape a case is in before its list is known. `S` hands the case file whole
to `$EDITOR` — a prompt and a fixture list are prose and paths, not a text area — and the pane re-reads
it when the editor exits.

**Facts ride in the assertion's own sentence.** `assertions` is a list of strings in the format, and the
judge reads them in that order, so what an expectation is checked against is written where the
expectation is: `every Sub-Topic appears exactly once — facts: ST-1 … ST-9, as in PS.md`. A second array
of facts would have to be kept in step by position, which is the one join this tool already distrusts.

**2 · iteration — work one iteration.** The left column is the iteration's **cases**, one row each, with
the case's own arithmetic in the right cell: `1/2` is one of its two assertions failed, `0/2` is a case
that passed every one, and the head counts the same over the iteration (`1/4 failed`). One row per case,
because `feedback.json` is keyed by slug and a case that passed every assertion is a case whose output
you can still review — a list of failures, the older shape, hid exactly that one.

The pane beside it is the case's own record and three named blocks, in the order the guide asks for them.
The record comes first, bare under the case's head — `prompt`, `expected_output`, `files`, the same three
rows the initialization view draws — because a review is a judgement of an output against the expected
output, and the screen that asks for one has no business hiding either; a window too short for it prints
one line naming `S`, which opens the file it came from. The window gives up the record first, then the
note, and never the table or the output. `ASSERTIONS` is the guide's own table: one row per declared
assertion, in the order the judge read
them, `pass` / `fail` / `—` under `WITH SKILL` and `WITHOUT SKILL`. `tab` moves the keys into the table
and `j k` walk its rows, passing rows included; the judge's evidence for the row under the cursor hangs
under that row — `without · the reply said so` — because evidence belongs to an assertion, and an
*evidence* block under a table is a sentence about a row the reader has to find again. Under the table
is the case's verdict on itself — `1 of 2 pass with the skill and fail without it — the rest measure the
model` — the loop's whole claim written per case rather than per rate. `OUTPUT` is the reply those
verdicts are about, capped at four lines, and `REVIEW` is the human's note.

**The review is per case and not per assertion**, which is the guide's split: an assertion is a sentence
*about* the output, and the output is what a person reviews. So `f` opens the same note from any row of
the table, and the note names no assertion — the row under the cursor is where the reader's eye is, not
what the note is written against. `ctrl+s` writes it into that iteration's `feedback.json` (backed up
first — it is a file in the workspace, not in the tree, and it is backed up like one).

**The set is editable here too**, with the initialization view's three keys: `e` opens the case's list
under the verdicts it is read against — one bullet per assertion, the caret in the sentence, `enter` a
new one, `ctrl+s` writes — `n` makes a case and `d` deletes one behind the same confirmation. Editing a
sentence is a replacement and not a second assertion: the file holds the list, so a rewritten assertion
is the same entry with new words, and the verdicts that were read against the old words no longer match
it (`g` re-grades, and the row reads `—` until it does).

The pane ends with one action line, the case's own next move: `g` while the iteration owes verdicts, then
`f` for the review, then `p` for a revision. `v` opens both arms' outputs (the same key as in the
initialization view); `enter` opens the arm's `activity.log` — how it got there, which is not drawn in
the pane at all: eight dim lines of tool calls under a table is a wall in front of the three things this
screen is for. `[` and `]` walk the iterations without leaving the section, `E` hands `SKILL.md` to
`$EDITOR` (a revision is a document, and a text field is not an editor) and `r` opens `report.md`.

```
 output optimization > dummy > iteration                                              evalctl v0.9.0
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 2 cases · 4 assertions · iteration #1 · 1 on disk · best #1
                                  │
  CASES               1/4 failed  │ ▌ dummy-2-three-dice                       #2 · 1 of 2 failed
  ─────────────────────────────   │   ──────────────────────────────────────────────────────────────
    CASE                   FAILED │   PATTERN         ASSERTION            WITH SKILL  WITHOUT SKILL
  ✓ dummy-1-one-die           0/2 │   ──────────────────────────────────────────────────────────────
▶ ✗ dummy-2-three-dice        1/2 │   make it fail    Three faces          pass         pass ✗
                                  │ ▶ adds value      The total adds up    pass         fail ✓
                                  │     without · the reply said so
                                  │   1 of 2 pass with the skill and fail without it — the rest measure the model
                                  │
                                  │   output · without the skill
                                  │   roll
                                  │
                                  │   REVIEW                                          one per case
                                  │   ──────────────────────────────────────────────────────────────
                                  │   the total was one off
                                  │
                                  │   → p  a revision of SKILL.md — the failures and the review are what it reads
```

That is the loop's middle, and it is the part that used to be a sentence in the guide: the failures and
the review were read into a pane that had no way to answer them. `f` is the answer, and it lands in the
same `feedback.json` the pane quotes. The assertions that passed are listed under the failures — the
case's whole list is what a revision is judged against, and one failure shown alone would make a
two-assertion case look like a one-assertion case.

**3 · testing quality — judge the trend.** Every iteration's two rates, the difference between them, how much
of the set ran, and how much of the grading list the judge saw. `enter` opens that iteration on the
iteration view — the question a table row raises is what happened in it, and the answer is its evidence, not its
own report (`r` opens `report.md`).

```
 output optimization > dummy > testing quality                                               evalctl v0.9.0 · 11:06
 3 cases · 6 assertions · iteration #2 · best #2

▌ ITERATIONS                                                       best = highest with-skill rate, complete only
    ITER    WITH WITHOUT Δ w−w/o   RAN   JUDGED  WHEN
▶     #1     89%     56%     +33   3/3      6/6  2026-11-01 12:00
      #2    100%     89%     +11   3/3      6/6  2026-11-01 14:20  best

  Δ = with − without, this iteration · RAN = cases run of 3 · JUDGED = assertions judged of 6
```

A rate is a percentage and nothing beside it: `89% 5/6` used to share one cell, and it reads as
arithmetic that does not hold — a rate that averages the cases is not the pooled assertions, so the two
numbers invite a division that lies about one of them. `JUDGED` carries the completeness fact in its own
column, `RAN` the set coverage, and both column names that cannot say their own basis have it spelled
out under the table: a delta between the arms and a delta against the last iteration are different
facts. A partial grade never ranks, the same rule the trigger lab enforces, and the best is the highest
with-skill rate among **complete** iterations — ties go to the later one.

The guide's stop condition is a fact here too: when the last two complete iterations tie, the table
says so, and the lab's `next:` line says "stop, or sharpen the assertions".

**The run grades itself.** `R` runs every case on both arms and then hands the arms to the judge —
one spend, two passes — so there is no step between the outputs and the verdicts to wait for. The live
screen says which pass it is in (`OUTPUT RUN · ITERATION 4`, then `GRADING · ITERATION 4`, then
`RUN + GRADED · ITERATION 4`), and the arm cells keep what the agent did while a `judging` state beside
them names the judge's pass: the same arm is not run twice. Verdicts go into each arm's `grading.json`,
then the benchmark and the report are re-aggregated.

`g` is what is left of the separate step, and it is two recoveries in one key: an iteration whose arms
carry no verdicts — a run made with `--no-grade`, by a build older than the fold, or one whose grade
failed — is graded by it, and an iteration that already has verdicts is re-graded by it **after asking**,
because that is the one thing an assertion edit leaves behind: the verdicts on screen describe the old
list. It grades **only the cases that owe a verdict** — a list rewritten since the grade, an arm nobody
judged, a case never graded at all — so one rewritten sentence is two judge calls and not two per case in
the iteration. Nothing is re-run: the arms' outputs are the evidence and the judge re-reads them. `evalctl run --no-grade` is the same trade from the shell. A finished run or grade lands on the
iteration view, because the next move after a spend is to read what failed.

## Where next

- [Carry one run's output into the next stage](promote-a-run.md)
- [What a run writes on disk](../reference/runs.md)
