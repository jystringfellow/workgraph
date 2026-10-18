# Feature Engineering pod

## Mission and seats

Finish a human-defined Workgraph change against its specification, executable
facts and compatibility/privacy requirements. Prefer small, sequential steps.

| Seat | Profile | Responsibility |
| --- | --- | --- |
| Lead | `wg-pod-lead` | Delegate, interpret evidence, enforce correction budgets, retain current handoff |
| Developer | `wg-pod-developer` | Write regression facts, implement, report finding-to-change dispositions |
| Independent reviewer | `wg-pod-reviewer` | Inspect primary sources, close findings and investigate regressions |
| Release gate | `wg-pod-gate` | Independently assess acceptance criteria and final readiness |

CAO profiles define provider, model and standing instructions. Run-specific aliases
preserve those assignments. The lead uses CAO assign/send_message and ends its
turn for callbacks; no built-in subagents, polling or model substitutions.

## Supervisor-driven execution protocol

1. Read AGENTS.md, the human brief, the plan sources, and host receipt. Reuse the
   plan while its hashes match. No architect activation is automatic. Missing
   requirements or changed plans stop for human clarification. Assess the whole
   requested feature, not only currently dirty files. Preserve pre-existing edits.
2. If implementation exists and baseline full checks pass, begin independent
   review. Otherwise assign initial developer fact-only work. For any behavior
   change, the developer writes/strengthens a fact and replies with its exact
   name, without changing implementation. Request host red verification; inspect
   its failure and the fact to establish the intended assertion failed. A setup
   failure is not a red fact. Then authorize implementation in the same seat.
3. After implementation, obtain a concrete disposition and compare repository
   evidence. Request full host verification. Failed checks require bounded
   corrections; they never authorize approval or provider fallback. No agent
   runs tests. Successful logs stay outside model context.
4. Give the reviewer the brief, source references, current HEAD/fingerprint and
   compact fresh verification evidence. The first assessment is independent of
   other reviewers' opinions. It must inspect correctness, privacy, lineage,
   failure paths, compatibility, edge cases and facts. Require concrete material
   findings with location, scenario, violated requirement and supporting evidence,
   or explicit approval of the supplied candidate. Ordinary prose is sufficient.
5. Permit at most **three developer review corrections**, each followed by host
   verification and a fresh independent assessment, including the third correction.
   This budget also covers verification corrections after initial implementation.
   Subsequent reviewers receive compact prior findings, developer dispositions
   and change identity, then independently verify closure and regressions.
6. Effective no-op corrections and changed-then-reverted corrections require an
   explicit disposition: corrected with change evidence, disputed with concrete
   repository/spec/test evidence, or unable to resolve. An unchanged candidate
   with unresolved or unsupported findings stops. The developer cannot dismiss
   its own finding; an independent assessment must accept any evidence-backed
   no-code-change resolution. Such an assessment remains within the assignment
   budget. No retry loop is created by a disagreement.
7. After full checks and independent approval, assign the gate. It independently
   inspects the full contract and current repository; another seat's approval is
   not proof. Require explicit readiness approval naming the same HEAD/fingerprint.
8. Permit at most **two developer gate corrections**. Behavioral corrections follow
   fact → host red → implementation → full host checks → fresh independent review
   → gate reassessment. Behavior-preserving refactors need proportionate checks
   without inventing a failing fact. Renewed review findings consume the remaining
   global three-review correction budget. Never reset budgets or create nested correction loops.
9. On missing/ambiguous assessments, startup/tool failure, uncertain execution,
   exhausted budgets or unsupported dispositions, retain final unresolved evidence
   in `handoff.md` and write `Stage: stopped`. Do not spend developer correction
   cycles repairing output formatting or retry a possibly side-effecting step.
10. Write `Stage: ready for human review` only after full current verification,
    independent review, and explicit gate approval of the same candidate. Include
    current identity, check receipt, findings and dispositions, both budgets,
    assignment count and terminal provenance. Keep history concise and identify
    current evidence clearly. Do not commit, push, publish or open a PR.

## Host protocol and bounds

The [runner](../pod.py) prepares a private execution packet under
`.cao/pod-runs/pod-<id>/`. `plan-state.json` binds AGENTS.md and explicit plan
sources. `seats.json` provides exact run-specific profile names. `checks.json`
is the latest host receipt; numbered receipts retain prior results. The prepared
`verify.sh` snapshot is read once and checked against the in-memory manifest hash
at run startup, before registration, baseline checks, or seat launches. All full
checks use those retained bytes; later disk edits cannot replace the command.

The lead requests checks by writing `verify.request` in that run directory:
`1 red TestExactFactName`, then `2 full`, increasing the number each time. End
its turn immediately; the host waits for every session terminal to be idle,
completed, or in error, runs the command, and sends a compact callback. Error
means yielded, not successful. The lead enforces red-first for behavioral changes
and confirms the intended assertion failed before authorizing implementation.
The host records changed paths relative to HEAD and red evidence, but does not
require a red receipt before full verification. Failed full checks can be corrected
and retried within the budgets; confirming green reds and behavior-preserving
refactor corrections do not prevent another full check.
Final readiness requires each independent seat's latest delivered callback to
name the candidate and follow the latest full receipt's inbox boundary, with
every session terminal yielded. Ready/stopped stage values must occupy the entire
line, with an optional trailing period; put explanations on separate lines.
Never edit check receipts, host configuration, plan bindings or seat profiles.
Read failing test locations for triage; stop for insufficient evidence.

The host additionally bounds the run to 14 worker assignments, 12 check requests
and 40 minutes of supervision. Individual checks are time-limited. Engineering
correction budgets are enforced by the lead's protocol; the host is not a
transactional workflow engine. Requested provider/model identity is recorded;
actual model identity may remain unverified and is not a reason to invent work.

No Jev decision authority or automatic fallback is enabled. Shadow telemetry
must not enter independent review. Native provider permissions remain distinct
from task instructions; prompt-only grants are not OS write isolation. Reviewers
must use host-supplied identity without requiring shell access to hash files.

Keep only curated, privacy-bounded findings and evidence. Do not save raw terminal
transcripts, complete provider prompts, secrets, environment values or source
diffs. Run evidence has no automatic expiration; source cleanup must not remove
it. Operator inspection of actual verdicts remains required before acceptance.
See [RIG.md](../RIG.md) for operation, limitations, recovery and retention.
