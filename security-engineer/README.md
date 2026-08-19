# Security Engineer — Take-Home Assignment

## Overview

One integrated scenario, not a grab bag of unrelated exercises. You'll triage a findings bundle, fix the highest-risk issue, and translate the result for two different audiences. This mirrors the actual job: findings arrive from scanners and pentests, you decide what's real and what's noise, you fix what you can, and you explain the outcome to people who don't share your context.

Breadth is not the goal. Spend most of your time on the top 1–3 risks. We will not reward exhaustive coverage. If you run out of time, write the remediation plan instead of forcing a second patch. A focused, well-reasoned pass on the highest-risk items beats a shallow sweep across everything in the bundle.

---

## AI Policy

AI tools are allowed. `ai-usage.md` is required from everyone, whether or not you used AI.

If you used AI, include:

1. Which tool you used
2. Prompt summaries or prompts
3. Suggestions you accepted
4. Suggestions you rejected
5. What you independently verified

If you did not use AI, just write "No AI tools used."

We are not evaluating whether you used AI. We are evaluating whether you used it responsibly — AI-generated security assessments and fixes can be confidently wrong in ways that are easy to miss if you don't verify them.

---

## Background

You're the Security Engineer embedded in a platform team. A findings bundle just landed from a mix of sources — a cloud posture scanner, a static analysis tool, and a recent pentest. Some of it is real. Some of it is noise, duplicates, or lower-priority than its severity label suggests. One item is something you have the context to fix yourself right now — you either fix it or explain why you're choosing not to.

## Repo Structure

```
security-review/
  findings.md
  app/
    (application code with the code-level findings)
  infra/
    network.tf
    secrets-config.yaml
```

---

## What's in the Bundle

`findings.md` contains several findings in a scanner-report style (severity, CWE/category, affected component, description), drawn from a mix of a cloud posture scanner, a static analysis tool, and a pentest. They span the application code and the infrastructure/config, and they vary in quality: some are genuine, some are overstated or understated relative to their label, and some may be noise, duplicates, or not actionable without more context. Deciding which is which — and backing each call with evidence from the actual code and config, not the scanner's severity — is the exercise. Do not assume the labels are correct.

---

## Deliverables

### 1. `security-triage-review.md`

```
## Findings, Ranked

### Detailed analysis required for top 3
For each:
- Severity (your assessment, not just the scanner's label)
- Exploitability / reachability — what would actually have to be true for this to be exploited
- Blast radius — data, users/tenants, environment, privileges, compensating controls
- False positive / duplicate / needs-more-context calls, with your reasoning
- Fix, mitigation, exception, or owner decision
- Validation evidence — how you'd confirm the fix actually closes the gap

### Brief disposition for remaining findings
One or two sentences each: fix later, duplicate, false positive, needs owner/context, or accepted risk.

## Open Questions
```

### 2. `patch.diff` or updated files, plus one remediation plan

Fix one highest-risk issue with a focused patch.

Also include one remediation plan for a second issue in the other domain:
- If your patch is code, write the infrastructure/config remediation plan.
- If your patch is infrastructure/config, write the code remediation plan.
- If the second fix is small and you have time, you may patch it too, but this is not required.

We care about prioritization and reasoning more than completeness — a minimal, safe, testable fix plus a well-reasoned plan beats two rushed patches.

### 3. `stakeholder-note.md`

Two sections, same underlying facts, two audiences:

- **Engineering action note** — what needs to change, why, how urgent, how to verify it's fixed. Written for the engineer who owns the affected code.
- **Audit-ready summary** — what the finding was, current status, evidence, and remediation timeline. Written for someone who needs an accurate, evidence-backed status update — not the engineering detail, and not spin.

### 4. `security-standard.md`

One short, concrete standard or guardrail that would prevent this class of issue from recurring. Include what the exception path looks like for teams that have a legitimate reason to deviate.

### 5. `ai-usage.md` (required from everyone)

See AI policy above.

---

## What We're Assessing

We look at: **triage judgment** (severity by real risk, not scanner label), **remediation quality** (minimal, safe fix that preserves the security model), **validation evidence** (how you'd prove the fix works), **stakeholder translation** (same facts, right register for engineer vs. auditor), **standards/coaching** (a guardrail plus an exception path), **AI usage** (responsible use — see the AI Policy), and **time/prioritization discipline** (depth on the top 1–3 risks over shallow breadth).

---

## Explicit Scope Notes

This exercise does **not** attempt to test:

- Full penetration-testing depth (multi-day exploitation).
- Staff/architect-level security strategy or org-wide program design.
- Sustained influence-without-authority or long-term standards adoption.

If you find yourself going deep on any of the above, stop — it's not what we're looking at.
