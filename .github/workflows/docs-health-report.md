---
name: Docs Health Report
on:
  schedule:
    - cron: "weekly on monday"
  workflow_dispatch:
    inputs:
      scope:
        description: >-
          Optional path prefix or glob-like hint to narrow the audit, such as
          `docs/`, `cli/azd/docs/`, or `cli/azd/pkg/project/`.
        required: false
        type: string
      max_findings:
        description: >-
          Maximum number of findings to report. Default: 10.
        required: false
        default: "10"
        type: string
      since:
        description: >-
          Optional date (YYYY-MM-DD) to bias the audit toward recently changed
          files and strings.
        required: false
        type: string
permissions:
  contents: read
  issues: read
  pull-requests: read
  actions: read
engine: copilot
network: defaults
tools:
  github:
    toolsets: [repos, issues, pull_requests, actions]
  edit:
safe-outputs:
  create-issue:
    max: 1
---

# Docs Health Report

Audit this repository for stale documentation and stale **user-facing strings**.
This workflow is meant to help with the docs-health goal tracked by issue
#7165, while also covering the important case where the stale content lives in
CLI/task/help/error text instead of Markdown docs.

Work from the checked-out repository contents plus GitHub repository metadata.
Read files locally with the **edit** tool. Use the **GitHub** tools only for
supplemental context such as recent PRs, recent workflow runs, or issue history.

## Scope

Audit the current repository tree, prioritizing these locations:

- `docs/**`
- `cli/azd/docs/**`
- top-level `*.md` files that contributors are likely to read
- user-facing strings under `cli/azd/**`, especially:
  - command help text
  - prompts
  - warnings
  - error messages
  - task/result headers
  - comments or strings that describe current product names, roles, or flows

If the **scope** input is provided, treat it as a strong hint for where to look
first and keep the audit centered there.

## What counts as a finding

Report only **high-confidence** drift. Good examples:

- a doc mentions a command, flag, path, workflow, or environment variable that
  no longer exists or has been renamed
- a user-facing string refers to an outdated product name, role name, feature
  name, or workflow step
- docs describe behavior that clearly differs from the implementation
- a help/error/prompt string sends the user to a stale file path, command, URL,
  or product term
- docs and user-facing strings disagree on the current name for the same thing

Do **not** report speculative style nits, general cleanup ideas, or issues that
cannot be justified with repository evidence.

## Audit approach

1. Resolve the effective audit scope from the **scope** input.
2. If the **since** input is set, bias the audit toward files that appear to be
   related to that time window; otherwise prioritize the current tree and the
   highest-signal user-facing surfaces.
3. Cross-check documentation claims against the source of truth in code,
   schemas, workflows, help text, and referenced file paths.
4. Treat user-facing strings as documentation too. When a string emitted to the
   user is stale, include it even if no Markdown file is involved.
5. Keep findings deduplicated. Prefer one finding per underlying drift theme,
   with the strongest supporting examples.
6. Cap the final report at **max_findings** findings, defaulting to 10 when the
   input is empty or invalid.

## Output

If you find no actionable drift, do **not** create an issue.

Otherwise, create **one** GitHub issue with a concise, scannable report.

Use this structure:

- Title: `Docs health report: stale docs and user-facing strings`
- Opening sentence that links the report to this workflow and explains the scope
- **Settings**
  - Scope used
  - Since value used (or `not set`)
  - Max findings used
- **Summary**
  - Total findings reported
  - Short note on which areas were inspected most heavily
- **Findings**
  - One bullet per finding
  - Each bullet must include:
    - affected file path(s)
    - the stale term/string/claim
    - why it appears outdated
    - the likely source of truth to use when fixing it
- **Suggested next steps**
  - short bullets grouping follow-up work when multiple findings share a theme

## Guardrails

- Be conservative: prefer missing a weak finding over filing a noisy issue.
- Never propose fixes without citing the file or string that triggered the
  finding.
- Do not modify repository files.
- Do not open more than one issue.
- Do not file issues for purely stylistic wording preferences.
- When a finding depends on runtime behavior, only report it if the repository
  contents make the mismatch clear.
