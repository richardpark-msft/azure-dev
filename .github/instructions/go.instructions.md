applyTo:
  - "**/*.go"
---
# Modern Go (1.26+) — PR Review Guidelines

This project uses **Go 1.26** (`cli/azd/go.mod`). Do not flag modern Go 1.26
features as errors.

## `new(expr)` creates typed pointers from values

`new(false)`, `new(true)`, `new(0)`, `new("s")` are **valid Go 1.26**. They
create a pointer to the given value. This replaces helper functions like
`to.Ptr(val)`. Do NOT suggest `boolPtr()` or `&localVar` replacements.

## Other modern patterns to accept (not flag)

- `errors.AsType[*T](err)` — generic error unwrapping (replaces `var e *T; errors.As(err, &e)`)
- `for i := range n` — range over integers
- `t.Context()` — test context (replaces `context.Background()` in tests)
- `t.Chdir(dir)` — test directory change (replaces `os.Chdir` + deferred restore)
- `wg.Go(func() { ... })` — WaitGroup shorthand (replaces `wg.Add(1); go func() { defer wg.Done(); ... }()`)
- `min()`, `max()`, `clear()` — built-in functions

## Review the full file, not just the diff

Before flagging missing imports or undefined references, verify the symbol isn't
already defined in unchanged portions of the file. The diff context may not show
all existing imports or declarations.

## MUST flag: duplicated functions or logic across files

You **MUST** leave an inline comment when two or more functions in the diff
implement the same logic with only superficial differences — e.g., two
near-identical helpers deriving a startup command, two unmarshalers reading
the same YAML, or the same regex/string-derivation pasted into multiple
files. Comment on the later occurrence and suggest extracting a single
shared helper.

This rule is broader than the duplicated-constants rule below: it covers
duplicated **functions**, **regex patterns**, **prompt flows**, and
**unmarshal logic**. The threshold is the same — two or more occurrences
with the same intent.

**Exception:** Do not flag genuinely-independent code that happens to use
similar literals (e.g., two unrelated tests with the same expected value),
or test-only fakes that mirror production interfaces by design.

_Sources: [jongio on #8161](https://github.com/Azure/azure-dev/pull/8161#discussion_r3237721645),
[jongio on #8146](https://github.com/Azure/azure-dev/pull/8146#discussion_r3230070683),
[jongio on #8210](https://github.com/Azure/azure-dev/pull/8210#discussion_r3253172373),
[jongio on #8189](https://github.com/Azure/azure-dev/pull/8189#discussion_r3253175098),
[jongio on #8029](https://github.com/Azure/azure-dev/pull/8029#discussion_r3182576892),
[trangevi on #8104](https://github.com/Azure/azure-dev/pull/8104#discussion_r3210454770)._

## MUST flag: silent fallbacks and silent error swallowing

You **MUST** leave an inline comment when code:

- Falls through to a fallback path on a non-`NotExist` `os.Stat` or network
  error without surfacing the original error (e.g., `EACCES` treated like
  file-not-found).
- Returns partial results from a paginated call when `HasMore` is true but
  there is no usable cursor, without logging a warning or returning an
  error to callers.
- Overwrites a previously-set value (e.g., a user-supplied `--name` flag)
  silently when an alternate source wins.
- Discards an error from a non-trivial helper without a comment explaining
  why it is safe to ignore.

In every case suggest one of: surface the error to the caller, add a
`log.Printf` diagnostic so `--debug` users can see it, or document in a
code comment why the silent path is intentional.

This rule is broader than the `ctx.Err()` recovery-path rule below — it
covers any silent override, silent partial result, or silent fallback,
regardless of cancellation.

_Sources: [jongio on #8203](https://github.com/Azure/azure-dev/pull/8203#discussion_r3253166129),
[jongio on #8189](https://github.com/Azure/azure-dev/pull/8189#discussion_r3253175100),
[jongio on #8198](https://github.com/Azure/azure-dev/pull/8198#discussion_r3255350877),
[jongio on #8095](https://github.com/Azure/azure-dev/pull/8095#discussion_r3202076841),
[jongio on #8083](https://github.com/Azure/azure-dev/pull/8083#discussion_r3230090655),
[wbreza on #7400](https://github.com/Azure/azure-dev/pull/7400#discussion_r3034706070)._

## MUST flag: missing path traversal validation when joining external paths

You **MUST** leave an inline comment when code uses `filepath.Join` (or
equivalent) to combine a destination directory with a path that originated
from an API response, archive entry, user input, or any other external
source, and does not validate that the resulting path stays under the
destination.

A path like `../../etc/foo` will escape the destination directory. Even
when the source is a first-party API, defense-in-depth requires either:

- A `filepath.Rel(destDir, joined)` check rejecting any result that starts
  with `..`, OR
- An existing helper such as `osutil.SafePath` or a zip-slip guard.

Note that `filepath.Join` cleans the result but does **not** prevent
traversal when the input begins with `..`.

_Sources: [jongio on #8029](https://github.com/Azure/azure-dev/pull/8029#discussion_r3182576884),
[#8130 discussion](https://github.com/Azure/azure-dev/pull/8130#discussion_r3256980947),
[wbreza on #7400](https://github.com/Azure/azure-dev/pull/7400#discussion_r3102576399)._

## MUST flag: stale Godoc when public behavior changes

You **MUST** leave an inline comment when a diff changes the observable
behavior of an exported function, method, or constant and the existing
Godoc above it still describes the old contract. Examples:

- A function gains a new fallback branch (e.g., a Cloud Shell path) but
  the Godoc still enumerates only the old paths.
- A constant's effective value or interpretation changes but the doc
  comment still cites the old value.
- A flag's meaning broadens but the help text and Godoc were not updated.

Suggest a one-sentence amendment that brings the doc back in sync with the
new behavior. Stale Godoc is worse than missing Godoc because it actively
misleads readers.

_Sources: [hemarina on #8459](https://github.com/Azure/azure-dev/pull/8459#discussion_r3337988140),
[v1212 on #8426](https://github.com/Azure/azure-dev/pull/8426#discussion_r3321827574),
[jongio on #8162](https://github.com/Azure/azure-dev/pull/8162#discussion_r3234632000)._

## Accept `log.Printf` for diagnostic info; flag `fmt.Println` used the same way

`cli/azd/main.go` wires the standard `log` package to `io.Discard` by
default. Output only surfaces when the user passes `--debug` or sets
`AZD_DEBUG_LOG`. As a result, `log.Printf` is the **idiomatic** way to
emit debug-only diagnostic information — auto-resolved values, fallback
notes, pagination warnings, stale-env-var hints — without polluting
stdout.

**Do NOT flag** `log.Printf` calls used for diagnostic info, even when the
same function also writes user-facing output via `fmt.Println` or the
`output.*` helpers. The two streams are intentionally separate.

**DO flag** `fmt.Println` (or other unconditional stdout writes) used to
emit purely diagnostic information that the user only needs when
troubleshooting. Suggest switching to `log.Printf` so normal runs stay
clean.

_Sources: [vhvb1989 on #8083](https://github.com/Azure/azure-dev/pull/8083#discussion_r3197482400),
[v1212 on #8426](https://github.com/Azure/azure-dev/pull/8426#discussion_r3321828793),
[jongio on #8189](https://github.com/Azure/azure-dev/pull/8189#discussion_r3253175100)._
