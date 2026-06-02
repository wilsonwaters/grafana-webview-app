# AI Development Methodology

How to use AI agents to implement feature tasks within a software project.

## Overview

Work flows from a project brief down to individual tasks, then through a loop of agent execution and human review:

**Project brief** → **Streams** (major workstreams or epics) → **Tasks** (S/M/L issues) → **Agent execution** → **Human review**

Each task is tracked as an issue on the project board (e.g. GitHub Issues, Jira, Linear). Issues contain human-readable acceptance criteria, completion criteria, and a **Context Files to Read First** list pointing the agent at the docs and code it needs.

A human selects which task to work on, launches an AI agent (e.g. Claude Code), points it at the issue, and reviews the output.

## Project Kickoff

Before any tasks can be executed, a project needs to be broken down. New projects start with a brief and are decomposed top-down into the structure above.

### Step 1 — Write the brief

Start with a clear statement of intent: what is being built, who uses it, what success looks like, and any hard constraints (tech stack, timeline, integrations, regulatory requirements). The brief does not need to be long, but it must be specific enough that two reasonable readers would agree on what's in scope.

If the brief comes in as a casual prompt ("build a mobile app for tracking workouts"), the first step is to expand it: ask the agent to draft the missing dimensions (target users, key features, platforms, integrations, data model sketch, success criteria) and refine that draft with the stakeholders before proceeding.

### Step 2 — Decompose into streams

A **stream** is a coherent unit of work — usually an epic or feature area — that can mostly proceed in parallel with other streams. Each stream should be self-contained enough that one engineer (or agent + reviewer pair) can own it end-to-end without constant cross-coordination.

Good streams have a clear outcome ("members can complete onboarding", "coaches can review assessment results"), an identifiable user, and a finite scope. Bad streams are vague themes ("UX improvements") or grab bags.

A prompt the agent can use to draft the breakdown:

```
Given this project brief: [paste brief]

Propose a stream decomposition. For each stream:
- Name and one-line outcome
- Major capabilities included
- Cross-stream dependencies
- Rough size (small / medium / large)

Flag anything in the brief that is ambiguous or under-specified.
```

Refine the output before treating it as final. Streams set the shape of the entire project — getting them right matters more than getting them fast.

### Step 3 — Create per-stream master plans

For each stream, create a master plan document that contains:

- **Goal** — what the stream delivers
- **Task list** — each task with a one-line summary, size (S/M/L), and dependency on prior tasks
- **Integration points** — how this stream meets other streams (shared data, navigation handoffs, auth boundaries, etc.)
- **Out of scope** — boundary against what won't be done in this stream
- **Open questions** — decisions deferred until a particular task is reached, with the task they block

The master plan is the contract for the stream. As tasks complete, mark their status. New tasks discovered along the way are added with a note about when and why they emerged.

### Step 4 — Decompose streams into tasks

Each task is written up using the Task Structure below and added to the project tracker. Aim for tasks of roughly 4–8 hours of average human developer effort (the **M** size). Split anything larger; combine anything trivially small with adjacent work.

**Prefer vertical slices.** Where possible, each task should deliver a thin end-to-end slice of functionality that produces something demonstrable — the kind of thing you could show at a sprint review. A task like "add a working onboarding name-and-email screen that posts to the API and shows a confirmation" is preferable to three sibling tasks like "build the form component", "wire up the API call", and "build the confirmation screen". The vertical slice gives you a runnable artifact at every step; the horizontal split leaves you with three half-built layers and nothing to show.

This isn't always achievable — foundational tasks (schema migrations, auth scaffolding, infrastructure-only ADRs) genuinely have no user-visible output, and shouldn't be forced into a slice shape. But when a task could go either way, choose the slice. Indicators that you're slicing well: each task closes with a screenshot or recording of working behaviour; each merged task is something a stakeholder could see and react to; the feature stays roughly functional after every merge rather than being broken until the last task lands.

Order tasks by dependency. Identify which must be sequential and which can proceed in parallel — parallelism lets multiple agents work concurrently on independent tasks without conflicts.

A prompt the agent can use to draft an individual task spec:

```
Read the master plan at [path] and the brief at [path].

Draft a complete task spec for task [N] using the Task Structure template
in the methodology doc. Be specific about Completion Criteria, Edge Cases,
and Context Files to Read First. Flag anything you had to assume.
```

Always refine drafted task specs before adding to the tracker. The spec is the contract for the task — ambiguity here is the single biggest driver of poor agent output.

### Step 5 — Begin execution

Once the master plan and first batch of task specs are in place, execution follows the loop in **How to Execute a Task** below. The master plan stays live throughout — revisit it as the project progresses.

## Task Structure

Each issue follows this general format. User-facing tasks include a user story; technical tasks use a plain description. All tasks include verifiable completion criteria.

Spec quality is the highest-leverage quality control in the system. Ambiguous specs produce ambiguous code — the more precise the issue, the better the agent output.

```markdown
## Description
What this task is about and why it matters.
(User-facing tasks may include: "As a [role], I can [action] so that [benefit]")

## Size
S | M | L (see Task Sizing below)

## Scope
- What's in scope
- What's out of scope

## Non-goals
- What this task explicitly does NOT do
- Behaviour deferred to other tasks (with issue references)

## Completion Criteria
- [ ] Specific, verifiable items
- [ ] Tests pass, files created, checks clean, etc.

## Edge Cases
- Enumerated scenarios the implementation must handle
- Error states, empty data, null responses, offline behaviour
- Boundary conditions and unexpected input

## Test Strategy
- **Unit:** What pure logic/hooks/utils to test, approximate count
- **Component:** What render + interaction tests, approximate count
- **Integration:** What cross-component flows, or "None (covered by integration verification point after task group)"

## Context Files to Read First
- List of files the agent must read before starting
- Project conventions docs, ADRs, related features, planning docs, reference code

## Notes
Context, links, design decisions
```

The agent is responsible for working out the implementation approach (which files to create or modify, which patterns to follow, etc.) by reading the listed context files and using plan mode.

## Accessibility in Specs

User-facing tasks should include task-specific a11y items in Completion Criteria. The blanket WCAG target lives in the project's conventions doc — specs only need to spell out what an agent cannot derive from generic rules. Typical items:

- **Focus management** on screen transitions, modal open/close, and async completion — which element gets focus and when
- **Live-region announcements** for state changes that would otherwise be silent (save status, errors, completions)
- **Accessible labels** for custom controls, with i18n keys where applicable
- **Contrast verification** in both light and dark themes for new coloured surfaces or state indicators

Skip what's default in the UI library or already covered by the project's review pass. Format as a short sub-checklist (3–5 items) inside Completion Criteria, with matching assertions added to Test Strategy.

## Task Sizing

Each task has a size (S/M/L) recorded in the issue body under `## Size`. This is plain text, not a tracker label.

| Size | Criteria | Gates |
|------|----------|-------|
| **S** | Single file, config, scaffold, ADR-only | Spec approval → middle loop → human review |
| **M** | Multi-file, single concern | Spec approval → plan approval → middle loop → human review |
| **L** | Cross-concern, complex UI, architectural | Spec approval → design approval → plan approval → middle loop → human review |

For **S** tasks, the agent can skip the plan-approval step and go straight to implementation. For **L** tasks, include a design discussion before planning.

## How to Execute a Task

### 1. Select a task

Choose a task from the project board. Check that its dependencies are complete (see the stream's master plan for the dependency graph).

### 2. Launch the AI agent

Open your AI coding agent (e.g. Claude Code) in the repository root.

### 3. Provide the task

Use this prompt pattern:

```
I want to implement issue #[NUMBER] for the [STREAM-NAME] stream.

Read the issue (use the appropriate CLI for your tracker, e.g. `gh issue view [NUMBER]`).

Then read the files listed in the issue's "Context Files to Read First" section.

Create a plan, then implement it following the methodology in <path-to-this-doc>.
```

Optionally, use plan mode or a structured build skill to organise the work.

### 4. Review the plan

The agent will create a plan. Review it before approving execution:
- Does it align with the completion criteria?
- Does it follow the patterns in the project's conventions docs?
- Are the right files being created/modified?

### 5. Agent executes (inner loop)

The agent works through its plan:
1. **Write tests** for the completion criteria
2. **Write code** to make tests pass
3. **Run tests** and iterate until passing
4. Repeat for each piece of functionality

### 6. Agent runs quality checks (middle loop)

After implementation, the agent runs the project's deterministic checks. These typically include type checking, linting, formatting, i18n completeness (if applicable), and unit/component tests. Each project should expose these as a single command or a documented sequence.

All checks must pass before proceeding. Once they pass, commit any fixes the checks produced before moving on to §6b.

### 6b. Project review skill

Run the project's review skill (if one exists) to check changes against project standards. This must be run directly by the agent (not delegated to a sub-agent) if the skill spawns its own sub-agents.

Address any findings before proceeding. Once findings are addressed and the deterministic checks from §6 still pass, commit the changes before moving on to §6c.

### 6c. AC-to-Test mapping verification

Before proceeding to human review, verify that every acceptance criterion has a corresponding test:

1. Re-read the Completion Criteria from the issue
2. For each criterion, identify the specific test file and test name that verifies it
3. If any criterion is not covered by a test, write the test
4. List the mapping (criterion → test) so the human reviewer can verify

For visible criteria (button labels, rendered states, loading/empty/error treatments), the runtime visual assertion in §6e also counts toward this mapping — the screenshot + text extraction can stand in for a unit test where the criterion is fundamentally about what the user sees.

### 6d. Spec adherence self-check

Re-read the issue. Verify:

1. Every Completion Criteria item is implemented
2. No functionality was added beyond what the spec requires (no unrequested features, helpers, or abstractions)
3. Nothing was skipped or deferred — if something genuinely cannot be done, note it explicitly with a reason

### 6e. Runtime verification (for tasks with UI or API changes)

For tasks that produce visible UI changes or affect runtime behaviour, verify the changes work in a running application via a browser-control MCP (e.g. Chrome DevTools MCP, Playwright MCP):

1. Confirm with the user that the dev environment is running (relevant frontends plus any backend services). If not, ask the user to start it before continuing — do not start servers yourself.
2. Navigate to the relevant page(s) in the browser and verify the feature works as a user would experience it
3. Check server logs (or ask the user to check) for errors, warnings, or unexpected behaviour
4. For multi-app projects, use isolated browser contexts/tabs per app so sessions and state don't leak between them

**What this catches that static tests don't:**
- Rendering issues, layout problems, missing styles
- API integration errors (auth failures, malformed requests, unexpected responses)
- Navigation and routing issues in the real app
- Console errors and runtime exceptions
- Visual correctness of the implemented feature

**When to skip:** Pure refactors with no visible change, config-only tasks, ADR-only tasks, or tasks where the dev environment is unavailable.

#### Runtime accessibility verification (Web)

When the change touches interactive UI on web:

- **Automated audit (required):** run axe-core or equivalent on each page touched; fail the loop on any `critical` or `serious` finding
- **Spec-anchored probes (when the spec has a11y items):** for each a11y criterion, perform a matching runtime check — e.g. verify focus lands on the expected element on mount, the aria-live region updates on the expected trigger, accessible names change with state
- **Visual review (optional, advisory):** screenshot affected pages; comment on focus-ring visibility, icon-alone meaning, error perceivability without colour. Log as advisory notes for the human reviewer; do not auto-apply

Native mobile (iOS, Android) a11y is not covered by automated runtime checks — it remains the responsibility of code-level review plus manual VoiceOver/TalkBack passes during human review.

#### Visual completion-criteria verification

For each Completion Criteria item that produces a visible artifact (e.g., "Submit button shows 'Submitting…' while the request is in flight", "Loading skeleton displayed while fetching"), navigate to that state in the browser, screenshot it, extract text, and assert the rendered UI matches what the criterion says.

This is criterion verification, not UX judgment — the spec is the ground truth. Failures indicate the implementation diverges from the spec; either fix the implementation or update the spec via a follow-up task. Do not "interpret" a criterion to fit what was built (anti-copout).

#### Optional: on-demand UX considerations

If a human reviewer wants advisory input on UI/UX quality, design-system consistency, copy clarity, or state coverage, run the project's UX-notes skill (if available) over the branch. Output is a list of *considerations* — not findings, not bugs — that the reviewer can read and decide which (if any) to act on. **Never run inside the middle loop. Never auto-apply suggestions.** To prompt the agent to address a specific consideration, copy that single item into a new prompt rather than pasting the full notes file.

### 7. Human reviews output

Review the changes:
- Do they meet all completion criteria?
- Do they follow project conventions?
- Are tests meaningful (not trivial assertions)?
- Check the AC-to-Test mapping from step 6c — are the tests actually verifying the criteria, or are they superficial?

**Anti-copout checklist:** Watch for these common agent avoidance patterns:
- [ ] **Pre-existing deflection**: Agent claims something was already broken/missing — verify it actually was
- [ ] **Scope creep escape**: Agent claims something is out of scope that IS in the completion criteria
- [ ] **Skip/defer**: "We can add this later" for items that are in the current task
- [ ] **Weaken the check**: Test assertions that are trivially true, or a lint rule/threshold was weakened to pass
- [ ] **Mock to pass**: Excessive mocking that encodes expected output rather than simulating real behaviour
- [ ] **Partial completion**: Most criteria met but one or two quietly dropped

### 8. Commit and mark complete

If satisfied, commit the changes and update the issue status.

## SDLC Loop Methodology

Work follows three nested loops:

### Inner Loop (fast, no human needed)
Write Test → Write Code → Run Tests → repeat

Goal: produce working code as quickly as possible. Each task is scoped to ~4–8 hours of average human developer effort.

### Middle Loop (automated quality gates)
After the inner loop produces working code:
- **Deterministic checks**: types, lint, format, i18n completeness, etc.
- **Test verification**: all tests pass, completion criteria covered
- **Skill review**: run the project's review skill (if one exists) directly (not as a sub-agent if it spawns its own). Address findings before proceeding.
- **Runtime verification**: for tasks with UI or API changes, ensure the dev environment is running (ask the user if not), then visually verify the feature via a browser-control MCP and check server logs for errors. See §6e for details.
- **Runtime accessibility verification (Web)**: for UI changes, run the a11y checks in §6e via a browser-control MCP.
- If any check fails, fix and re-run (back to inner loop)

### Outer Loop (human review + integration)
- Human reviews the output
- Integration with other completed tasks is verified
- Changes are committed and deployed

## Cross-Task Integration

After completing multiple related tasks, verify they work together:
1. Check the stream's master plan for integration points
2. Run all tests across the feature
3. With the user's dev environment running, walk through the integration flow in the browser via a browser-control MCP — navigate the full user journey across the completed tasks and check (or ask the user to check) server logs for errors
4. For cross-app integration (e.g. one user role takes an action, another role views the result), verify in separate browser tabs using their isolated contexts

A human (or a review agent) can periodically check: "Review the current state of the [STREAM-NAME] stream against the master plan. Check that completed tasks integrate correctly and flag any issues."

## Optional Tools

- **Structured build skills**: helpful for plan creation and execution within a session
- **Review skills**: can review completed work against criteria
- **Ship/commit skills**: can commit and create PRs
- **Plan mode**: agents with plan mode help structure work before executing

## Planning Documents

Project planning and state lives in `ai-state/` at the repo root, committed directly to `main`. Keeping it on the main branch means every agent (orchestrator, implementation, review, runtime verification) can see the current plan and state without checking out a side branch, and stakeholders can read the project's current narrative directly on the default branch.

Typical structure:

- `ai-state/brief.md` — the project brief
- `ai-state/streams.md` — stream decomposition and status
- `ai-state/streams/<name>/master-plan.md` — per-stream master plan (task list, dependencies, integration points)
- `ai-state/streams/<name>/feature-analysis.md` — analysis of existing functionality being ported or replaced (if applicable)
- `ai-state/streams/<name>/data-schema-proposal.md` — API/data schema proposals
- `ai-state/streams/<name>/architecture-notes.md` — key decisions and patterns
- `ai-state/PROGRESS.md` — narrative log of project status (primarily maintained by the orchestrator agent)
- `ai-state/OPEN-QUESTIONS.md` — unresolved decisions and what they block
