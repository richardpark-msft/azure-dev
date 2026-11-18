# Charm UX playground

Standalone, disposable terminal demos, separate from the `azd` command tree.
Uses the existing Go module and Charm dependencies. All account, region,
and loading data is simulated. No authentication, network calls, file
writes, or Azure resources. Drafts are held in memory until you quit.

From `cli/azd`:

```sh
go run ./tools/ux-playground
```

Or build and run:

```sh
go build -o tools/ux-playground/ux-playground ./tools/ux-playground
./tools/ux-playground/ux-playground
```

The app uses the alternate screen and restores your terminal on exit.
`NO_COLOR=1` disables colors while keeping markers and borders.

## Demos

| Key | Demo | Try this |
|---|---|---|
| 1 | Resource builder | Add resources to populate the tree. Up/Down moves through visible rows, Left/Right collapses/expands a group (Left goes to the parent from a resource), and Enter edits a resource or toggles a group. Click the Add/Edit/Remove buttons or Tab to them and use Left/Right and Enter. `a`, `e`, `d` remain shortcuts; Edit/Remove require a resource selection. `l` changes location. Escape cancels an editor without changing the tree. |
| 2 | Live summary | Edit the name, location, and hosting. Watch the summary update before submitting. |
| 3 | Conditional sections | Select Container Apps or Functions, then advance. Only the matching settings page appears. Go back with Shift+Tab to change hosting. |
| 4 | Rich choices | Explore region names, identifiers, and geographic labels. Enter selects a region. |
| 5 | Inline loading and retry | A bordered panel separates concurrent lookups with divided rows and azd status markers. Locations and tenant details deliberately fail. Arrows select a check, `r` retries the selected failure, `a` retries all failures in parallel. Successful and loading checks are not retried. Focus stays put as results arrive. Enter restarts with failures, `l` demonstrates continued interaction. |
| 6 | Review before apply | Complete the form, review the proposed changes, `e` edits, `a` simulates applying. |
| 7 | Keyboard command palette | Ctrl+P opens a searchable palette. Type, use arrows, and Enter to jump to a demo. F1 opens contextual help. |
| 8 | Responsive layout | Resize across 90 columns. The summary moves beside the form on wide terminals and below it on narrow ones. |

Use Tab/Enter and Shift+Tab in Huh forms, arrows in selectors.
All demos, nested editors, the command palette, and contextual help have bordered
panels. Borders and focus markers remain visible without color.
Resource action buttons support terminal mouse reporting. Mouse support is enabled
only on the resource list, not inside the nested Huh editor. Narrow terminals wrap
the buttons onto additional rows.
Escape returns to the gallery. Ctrl+C quits from anywhere.
Ctrl+Up/Down scroll tall screens, useful on short terminals or after adding many
resources. The footer reports the visible line range when content overflows.

The resource builder intentionally demonstrates custom Bubble Tea navigation
around Huh add/edit screens. Conditional fields demonstrate Huh's group hiding,
not arbitrary live field insertion. Other demos use small custom Bubble Tea
views and Lip Gloss layouts. These are exploratory interactions, not proposed
changes to azd's production UX or real Azure capacity guidance.

## Validation

```sh
go test -race ./tools/ux-playground
go vet ./tools/ux-playground
```
