applyTo:
  - cli/azd/extensions/**
---
- When accessing a `Subscription` from `PromptSubscription()`, always use
  `Subscription.UserTenantId` (user access tenant) for credential creation,
  NOT `Subscription.TenantId` (resource tenant). For multi-tenant/guest users
  these differ, and using `TenantId` causes authentication failures.
  The `LookupTenant()` API already returns the correct user access tenant.

## Move spell-dictionary entries to the extension-scoped `cspell.yaml`

Each extension owns its own `cspell.yaml` (e.g.,
`cli/azd/extensions/azure.ai.agents/cspell.yaml`). Domain-specific words
introduced by an extension — API resource names, branded terms,
extension-only identifiers — belong in that extension's local
`cspell.yaml`, not in the shared `cli/azd/.vscode/cspell.yaml`.

Flag any diff that adds extension-only terms to the global cspell file and
suggest moving the entries into the appropriate extension's local
`cspell.yaml`. This rule complements the existing file-scoped cspell
guidance in `cli/azd/AGENTS.md`: words that are only used inside one
extension should not leak into the repo-wide dictionary.

_Sources: [trangevi on #8223](https://github.com/Azure/azure-dev/pull/8223#discussion_r3267887595),
[trangevi on #8174](https://github.com/Azure/azure-dev/pull/8174#discussion_r3250809537)._

## Pin extension `go.mod` Go versions to the repo source-of-truth

Every extension's `go.mod` declares its own Go toolchain version, but
those versions **must** match `cli/azd/go.mod`. The repo runs the
`validate-go-version` workflow against `cli/azd/go.mod` as the canonical
source. A mismatch — e.g., a new extension declaring `go 1.26.2` when the
repo is on `go 1.26.1` — will break CI or, worse, silently introduce
toolchain skew across extensions.

Flag any new or modified extension `go.mod` whose `go` directive does not
match `cli/azd/go.mod`. Suggest aligning to the canonical version and cite
the source-of-truth file.

_Sources: [jongio on #8219](https://github.com/Azure/azure-dev/pull/8219#discussion_r3253162998),
[jongio on #8130](https://github.com/Azure/azure-dev/pull/8130#discussion_r3230078169),
[jongio on #7400](https://github.com/Azure/azure-dev/pull/7400#discussion_r3031404433)._
