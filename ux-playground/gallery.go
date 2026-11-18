// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/charmbracelet/x/ansi"
)

var demos = []string{
	"Resource builder", "Live summary", "Conditional sections", "Rich choices",
	"Inline loading and retry", "Review before apply", "Keyboard command palette", "Responsive layout",
}

type resource struct {
	name string
	kind string
}

type resourceTreeRow struct {
	group string
	index int // -1 for a group heading
	count int
}

type lookupResult struct {
	generation int
	index      int
	failed     bool
}

type buttonHit struct {
	x, y, width, height int
	action              int
}

type gallery struct {
	buttons       []buttonHit // resource action hit regions in the last rendered terminal view
	actionFocus   bool
	action        int
	demo          int // zero is the gallery menu, otherwise one-based demo index
	cursor        int
	width         int
	height        int
	scroll        int
	form          *huh.Form
	name          string
	location      string
	host          string
	image         string
	runtime       string
	resources     []resource
	resourceGroup string          // nonempty when a group heading has focus instead of a resource
	collapsed     map[string]bool // expansion state by resource group name
	draft         resource
	edit          int // -1 adds a resource, otherwise replaces that resource's index
	review        bool
	applied       bool
	confirm       bool
	region        int
	loads         [3]string
	generation    int // ignores delayed lookup results from a previous demo or retry
	palette       bool
	paletteCursor int
	query         string
	help          bool
	status        string
}

func newGallery() *gallery {
	return &gallery{
		width: 80, height: 24, name: "playground", location: "eastus2",
		host: "Container Apps", image: "ghcr.io/example/web:latest", runtime: "Node.js 22",
		edit:      -1,
		collapsed: make(map[string]bool),
	}
}

func (m *gallery) Init() tea.Cmd { return nil }

func (m *gallery) open(index int) tea.Cmd {
	m.demo, m.cursor, m.scroll = index, 0, 0
	m.actionFocus, m.action = false, 0
	m.resourceGroup = ""
	if index == 1 && len(m.resources) > 0 {
		m.selectResource(0)
	}
	m.form, m.review, m.applied = nil, false, false
	m.palette, m.help, m.status = false, false, ""
	m.generation++
	switch index {
	case 2, 3, 6, 8:
		m.configForm(index == 3)
		return m.form.Init()
	case 5:
		return m.startLookups()
	}
	return nil
}

func (m *gallery) configForm(conditional bool) {
	m.confirm = true
	main := huh.NewGroup(
		huh.NewInput().Title("? Environment name").Value(&m.name).Validate(required),
		huh.NewSelect[string]().Title("? Location").Value(&m.location).
			Options(huh.NewOption("East US 2", "eastus2"), huh.NewOption("West Europe", "westeurope")),
		huh.NewSelect[string]().Title("? Hosting").Value(&m.host).
			Options(huh.NewOption("Container Apps", "Container Apps"), huh.NewOption("Functions", "Functions")),
	)
	if conditional {
		m.form = huh.NewForm(main,
			huh.NewGroup(huh.NewInput().Title("? Container image").Value(&m.image).Validate(required)).
				WithHideFunc(func() bool { return m.host != "Container Apps" }),
			huh.NewGroup(huh.NewSelect[string]().Title("? Function runtime").Value(&m.runtime).
				Options(huh.NewOption("Node.js 22", "Node.js 22"), huh.NewOption("Python 3.12", "Python 3.12"))).
				WithHideFunc(func() bool { return m.host != "Functions" }),
			huh.NewGroup(huh.NewConfirm().Title("? Keep this draft?").Value(&m.confirm)),
		)
	} else {
		m.form = huh.NewForm(main,
			huh.NewGroup(huh.NewConfirm().Title("? Review this draft?").Value(&m.confirm)),
		)
	}
	m.sizeForm()
}

func required(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("enter a non-empty value")
	}
	return nil
}

func (m *gallery) formWidth() int {
	if m.demo == 8 && m.width >= 90 {
		return max(12, (m.width-10)/2)
	}
	return max(12, m.width-6)
}

func (m *gallery) sizeForm() {
	if m.form != nil {
		height := max(5, m.height-12)
		if (m.demo == 2 || m.demo == 8) && !(m.demo == 8 && m.width >= 90) {
			height = max(5, m.height-18)
		}
		m.form.WithTheme(huh.ThemeFunc(huh.ThemeBase)).
			WithWidth(m.formWidth()).WithHeight(height)
	}
}

func (m *gallery) editResource(index int) tea.Cmd {
	m.scroll = 0
	m.edit = index
	m.draft = resource{kind: "Container App"}
	if index >= 0 {
		m.draft = m.resources[index]
	}
	m.confirm = true
	m.form = huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("? Resource name").Value(&m.draft.name).Validate(required),
		huh.NewSelect[string]().Title("? Resource type").Value(&m.draft.kind).
			Options(huh.NewOption("Container App", "Container App"),
				huh.NewOption("Storage Account", "Storage Account"), huh.NewOption("PostgreSQL", "PostgreSQL")),
		huh.NewConfirm().Title("? Save resource?").Value(&m.confirm),
	))
	m.sizeForm()
	return m.form.Init()
}

func lookup(generation, index int, fail bool) tea.Cmd {
	return tea.Tick(time.Duration(index+1)*700*time.Millisecond, func(time.Time) tea.Msg {
		return lookupResult{generation: generation, index: index, failed: fail}
	})
}

func (m *gallery) startLookups() tea.Cmd {
	m.loads = [3]string{"Loading...", "Loading...", "Loading..."}
	return tea.Batch(lookup(m.generation, 0, false), lookup(m.generation, 1, true), lookup(m.generation, 2, true))
}

func (m *gallery) retryCheck(index int) tea.Cmd {
	if !strings.HasPrefix(m.loads[index], "Failed:") {
		return nil
	}
	m.loads[index] = "Loading..."
	return lookup(m.generation, index, false)
}

func (m *gallery) paletteMatches() []int {
	var indices []int
	for i, name := range demos {
		if strings.Contains(strings.ToLower(name), strings.ToLower(m.query)) {
			indices = append(indices, i+1)
		}
	}
	return indices
}

func (m *gallery) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if m.demo == 1 && m.form == nil && !m.palette && !m.help && msg.Button == tea.MouseLeft {
			for _, button := range m.buttons {
				if msg.X >= button.x && msg.X < button.x+button.width &&
					msg.Y >= button.y && msg.Y < button.y+button.height {
					m.actionFocus, m.action = true, button.action
					return m, m.updateScreen([]string{"a", "e", "d"}[button.action])
				}
			}
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeForm()
	case lookupResult:
		if msg.generation == m.generation && m.demo == 5 {
			m.loads[msg.index] = "Ready"
			if msg.failed {
				m.loads[msg.index] = "Failed: simulated timeout. Press r to retry."
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.palette {
			return m, m.updatePalette(msg)
		}
		if m.help {
			if key == "f1" || key == "esc" {
				m.help = false
			}
			return m, nil
		}
		switch key {
		case "esc":
			if m.form != nil && m.demo == 1 {
				m.form = nil
				m.status = "Resource edit cancelled. List unchanged."
				m.revealResourceFocus()
				return m, nil
			}
			return m, m.open(0)
		case "ctrl+p":
			m.palette, m.query, m.paletteCursor = true, "", 0
			return m, nil
		case "f1":
			m.help = !m.help
			return m, nil
		case "ctrl+down":
			m.scroll++
			return m, nil
		case "ctrl+up":
			m.scroll = max(0, m.scroll-1)
			return m, nil
		}
		if m.form == nil {
			return m, m.updateScreen(key)
		}
	}
	if m.form != nil {
		_, command := m.form.Update(msg)
		if m.form.State == huh.StateCompleted {
			if m.demo == 1 && m.confirm {
				if m.edit < 0 {
					m.resources = append(m.resources, m.draft)
					m.selectResource(len(m.resources) - 1)
				} else {
					m.resources[m.edit] = m.draft
					m.selectResource(m.edit)
				}
				m.status = "Resource saved in memory."
			}
			if m.demo != 1 {
				m.review = m.confirm
				m.status = ""
				if !m.confirm {
					m.status = "Draft cancelled."
				}
			}
			m.form = nil
			if m.demo == 1 {
				m.revealResourceFocus()
			}
			return m, nil
		}
		return m, command
	}
	return m, nil
}

func (m *gallery) updatePalette(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.palette = false
	case "up":
		m.paletteCursor = max(0, m.paletteCursor-1)
	case "down":
		m.paletteCursor = min(max(0, len(m.paletteMatches())-1), m.paletteCursor+1)
	case "backspace":
		runes := []rune(m.query)
		if len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
		m.paletteCursor = 0
	case "enter":
		indices := m.paletteMatches()
		if len(indices) > 0 {
			return m.open(indices[m.paletteCursor])
		}
	default:
		if msg.Text != "" {
			m.query += msg.Text
			m.paletteCursor = 0
		}
	}
	return nil
}

func (m *gallery) updateScreen(key string) tea.Cmd {
	if m.demo == 0 || m.demo == 7 {
		switch key {
		case "q":
			return tea.Quit
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(demos)-1, m.cursor+1)
		case "enter":
			return m.open(m.cursor + 1)
		default:
			if len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
				return m.open(int(key[0] - '0'))
			}
		}
	}
	switch m.demo {
	case 1:
		switch key {
		case "tab", "shift+tab":
			m.actionFocus = !m.actionFocus
			m.revealResourceFocus()
		case "left":
			if m.actionFocus {
				m.action = max(0, m.action-1)
			} else {
				m.moveResourceBranch(false)
			}
		case "right":
			if m.actionFocus {
				m.action = min(2, m.action+1)
			} else {
				m.moveResourceBranch(true)
			}
		case "a":
			return m.editResource(-1)
		case "up", "down":
			if !m.actionFocus {
				m.moveResourceRow(key == "down")
			}
		case "e", "enter":
			if key == "enter" && m.actionFocus {
				return m.updateScreen([]string{"a", "e", "d"}[m.action])
			}
			if m.hasSelectedResource() {
				return m.editResource(m.cursor)
			}
			if key == "enter" && m.resourceGroup != "" {
				m.collapsed[m.resourceGroup] = !m.collapsed[m.resourceGroup]
				m.revealResourceRow()
			}
		case "d":
			if m.hasSelectedResource() {
				m.resources = slices.Delete(m.resources, m.cursor, m.cursor+1)
				m.cursor = min(m.cursor, max(0, len(m.resources)-1))
				if len(m.resources) > 0 {
					m.selectResource(m.cursor)
				}
				m.status = "Resource removed from the in-memory draft."
			}
		case "l":
			if m.location == "eastus2" {
				m.location = "westeurope"
			} else {
				m.location = "eastus2"
			}
		}
	case 4:
		switch key {
		case "up":
			m.region = max(0, m.region-1)
		case "down":
			m.region = min(2, m.region+1)
		case "enter":
			m.location = []string{"eastus2", "westeurope", "japaneast"}[m.region]
			m.status = "Selected " + m.location
		}
	case 5:
		switch key {
		case "up":
			m.cursor = max(0, m.cursor-1)
		case "down":
			m.cursor = min(len(m.loads)-1, m.cursor+1)
		case "r":
			command := m.retryCheck(m.cursor)
			if command == nil {
				m.status = "Select a failed check to retry."
			} else {
				m.status = "Retrying selected check."
			}
			return command
		case "a":
			var commands []tea.Cmd
			for i := range m.loads {
				if command := m.retryCheck(i); command != nil {
					commands = append(commands, command)
				}
			}
			m.status = fmt.Sprintf("Retrying %d failed checks.", len(commands))
			return tea.Batch(commands...)
		case "l":
			m.status = "You can keep interacting while lookups run."
		case "enter":
			m.generation++
			return m.startLookups()
		}
	case 2, 3, 6, 8:
		switch key {
		case "e":
			m.review, m.applied = false, false
			m.configForm(m.demo == 3)
			return m.form.Init()
		case "a":
			if m.review {
				m.applied = true
				m.status = "Simulated apply complete. No files written or Azure resources created."
			}
		}
	}
	return nil
}

func (m *gallery) summary() string {
	text := fmt.Sprintf("LIVE DRAFT\n\nName: %s\nLocation: %s\nHost: %s", m.name, m.location, m.host)
	if m.host == "Container Apps" {
		text += "\nImage: " + m.image
	} else {
		text += "\nRuntime: " + m.runtime
	}
	return text + fmt.Sprintf("\nResources: %d\n\nAll data is simulated.", len(m.resources))
}

func (m *gallery) menu() string {
	lines := []string{"Choose a demo with arrows + Enter, or press 1-8."}
	for i, name := range demos {
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%d. %s", marker, i+1, name))
	}
	return strings.Join(lines, "\n")
}

func borderedPanel(content string, width int) string {
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).
		Width(max(1, width-4)).Render(content)
}

func (m *gallery) body() string {
	content := m.demoContent()
	if !m.palette && (m.form != nil || m.demo == 5) {
		return content
	}
	return borderedPanel(content, m.width)
}

func (m *gallery) demoContent() string {
	if m.palette {
		lines := []string{"COMMAND PALETTE", "Search: " + m.query + "_"}
		for i, index := range m.paletteMatches() {
			prefix := "  "
			if i == m.paletteCursor {
				prefix = "> "
			}
			lines = append(lines, prefix+demos[index-1])
		}
		if len(m.paletteMatches()) == 0 {
			lines = append(lines, "No matching demos. Backspace to change the search.")
		}
		return strings.Join(lines, "\n")
	}
	if m.demo == 0 || m.demo == 7 {
		return m.menu() + "\n\nCtrl+P: searchable command palette. F1: contextual help."
	}
	if m.form != nil {
		form := borderedPanel(m.form.View(), m.formWidth()+4)
		if m.demo == 2 || m.demo == 8 {
			summary := m.summary()
			if !(m.demo == 8 && m.width >= 90) {
				summary = fmt.Sprintf("LIVE: %s | %s\nHosting: %s", m.name, m.location, m.host)
			}
			panel := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).
				Width(max(12, m.formWidth()-2)).Render(summary)
			if m.demo == 8 && m.width >= 90 {
				return lipgloss.JoinHorizontal(lipgloss.Top, form, "  ", panel)
			}
			return form + "\n\n" + panel
		}
		return form
	}
	switch m.demo {
	case 1:
		return "Location: " + m.location + "   [l: change]\n\n" + m.resourceTree() + "\n\n" + m.resourceButtons() +
			"\nUp/Down: move | Left/Right: branches or actions\nTab: tree/actions | Enter: activate"
	case 4:
		regions := []string{
			"East US 2    | eastus2    | Virginia, United States",
			"West Europe  | westeurope | Netherlands",
			"Japan East   | japaneast  | Tokyo, Japan",
		}
		for i := range regions {
			prefix := "  "
			if i == m.region {
				prefix = "> "
			}
			regions[i] = prefix + regions[i]
		}
		return "REGION DETAILS (SAMPLE DATA)\n\n" + strings.Join(regions, "\n\n") +
			"\n\nArrows: explore | Enter: select."
	case 5:
		return m.loadingPanel() + "\nUp/Down: select | R: retry selected | A: retry failed\nEnter: restart | l: interact"
	default:
		if !m.review {
			return "Draft cancelled. Press e to start again."
		}
		title := "REVIEW BEFORE APPLY"
		if m.applied {
			title = "SIMULATED APPLY COMPLETE"
		}
		return title + "\n\n+ Create environment: " + m.name + "\n+ Set location: " + m.location +
			"\n+ Configure hosting: " + m.host + "\n\n" + m.summary() +
			"\n\ne: edit draft | a: simulate apply | Esc: gallery"
	}
}

var resourceActions = []string{"Add resource [A]", "Edit [E]", "Remove [D]"}

func resourceCategory(kind string) string {
	switch kind {
	case "Container App":
		return "Compute"
	case "Storage Account":
		return "Storage"
	case "PostgreSQL":
		return "Databases"
	default:
		return kind
	}
}

func (m *gallery) resourceRows() []resourceTreeRow {
	groups := make(map[string][]int)
	var order []string
	for index, res := range m.resources {
		group := resourceCategory(res.kind)
		if len(groups[group]) == 0 {
			order = append(order, group)
		}
		groups[group] = append(groups[group], index)
	}
	var rows []resourceTreeRow
	for _, group := range order {
		rows = append(rows, resourceTreeRow{group: group, index: -1, count: len(groups[group])})
		if !m.collapsed[group] {
			for _, index := range groups[group] {
				rows = append(rows, resourceTreeRow{group: group, index: index})
			}
		}
	}
	return rows
}

func (m *gallery) hasSelectedResource() bool {
	return m.resourceGroup == "" && m.cursor < len(m.resources)
}

func (m *gallery) selectResource(index int) {
	m.resourceGroup, m.cursor = "", index
	delete(m.collapsed, resourceCategory(m.resources[index].kind))
	if !m.actionFocus {
		m.revealResourceRow()
	}
}

func (m *gallery) selectedResourceRow(row resourceTreeRow) bool {
	if m.resourceGroup != "" {
		return row.index == -1 && m.resourceGroup == row.group
	}
	return row.index == m.cursor
}

func (m *gallery) moveResourceRow(down bool) {
	rows := m.resourceRows()
	index := slices.IndexFunc(rows, m.selectedResourceRow)
	if index == -1 {
		return
	}
	if down {
		index = min(index+1, len(rows)-1)
	} else {
		index = max(index-1, 0)
	}
	row := rows[index]
	if row.index == -1 {
		m.resourceGroup = row.group
	} else {
		m.resourceGroup, m.cursor = "", row.index
	}
	m.revealResourceRow()
}

func (m *gallery) moveResourceBranch(expand bool) {
	if m.resourceGroup != "" {
		m.collapsed[m.resourceGroup] = !expand
	} else if !expand && m.hasSelectedResource() {
		m.resourceGroup = resourceCategory(m.resources[m.cursor].kind)
	}
	m.revealResourceRow()
}

func (m *gallery) revealResourceRow() {
	if index := slices.IndexFunc(m.resourceRows(), m.selectedResourceRow); index >= 0 {
		line := index + 4 // border, location, blank line, and tree root
		available := max(1, m.height-6)
		m.scroll = max(0, max(line-available+1, min(m.scroll, line)))
	}
}

func (m *gallery) revealResourceFocus() {
	if m.actionFocus {
		m.scroll = strings.Count(m.body(), "\n") + 1
	} else {
		m.revealResourceRow()
	}
}

func (m *gallery) resourceTree() string {
	if len(m.resources) == 0 {
		return "Resources\nNo resources yet. Press a to open an add form."
	}
	outline := tree.Root("Resources")
	var branch *tree.Tree
	for _, row := range m.resourceRows() {
		marker := "  "
		if !m.actionFocus && m.selectedResourceRow(row) {
			marker = output.WithHighLightFormat("> ")
		}
		if row.index == -1 {
			arrow := "▾"
			if m.collapsed[row.group] {
				arrow = "▸"
			}
			branch = tree.Root(fmt.Sprintf("%s%s %s (%d)", marker, arrow, row.group, row.count))
			outline.Child(branch)
		} else {
			res := m.resources[row.index]
			branch.Child(fmt.Sprintf("%s%s (%s)", marker, res.name, res.kind))
		}
	}
	return outline.String()
}

func (m *gallery) resourceButtons() string {
	var rows []string
	row := ""
	for i, label := range resourceActions {
		style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
		if m.actionFocus && m.action == i {
			style = style.Border(lipgloss.ThickBorder())
		}
		if i > 0 && !m.hasSelectedResource() {
			label += " (disabled)"
		}
		button := style.Render(label)
		if row != "" && lipgloss.Width(row)+1+lipgloss.Width(button) > max(1, m.width-4) {
			rows = append(rows, row)
			row = ""
		}
		if row == "" {
			row = button
		} else {
			row = lipgloss.JoinHorizontal(lipgloss.Top, row, " ", button)
		}
	}
	return strings.Join(append(rows, row), "\n")
}

func (m *gallery) loadingPanel() string {
	width := max(1, min(76, m.width)-4)
	divider := strings.Repeat("─", width)
	parts := []string{output.WithBold("AZURE ACCOUNT CHECKS"), divider}
	for i, name := range []string{"subscription", "locations", "tenant details"} {
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		status := output.WithGrayFormat("|===    | Loading " + name)
		detail, action := "", ""
		switch {
		case m.loads[i] == "Ready":
			status = output.WithSuccessFormat("(✓) Done: Loaded " + name)
		case strings.HasPrefix(m.loads[i], "Failed:"):
			status = output.WithErrorFormat("(x) Failed: Loading " + name)
			detail = output.WithErrorFormat("    Simulated request timeout.")
			if i == m.cursor {
				action = output.WithHighLightFormat("    [R] Retry this check")
			}
		}
		// Reserve error and action lines so completion does not move adjacent rows.
		row := lipgloss.NewStyle().Width(width).
			Render(strings.Join([]string{marker + status, detail, action}, "\n"))
		parts = append(parts, row)
		if i < len(m.loads)-1 {
			parts = append(parts, divider)
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).
		Width(width).Render(strings.Join(parts, "\n"))
}

func (m *gallery) View() tea.View {
	title := "Charm UX playground"
	if m.demo > 0 {
		title += " / " + demos[m.demo-1]
	}
	body := m.body()
	if m.help {
		body = borderedPanel("HELP\n\n"+demosHelp(m.demo)+
			"\n\nEsc or F1: close help\n"+
			"Ctrl+Up/Down: scroll tall content\nCtrl+C: quit", m.width)
	}
	lines := strings.Split(body, "\n")
	available := max(1, m.height-6)
	start := min(m.scroll, max(0, len(lines)-available))
	end := min(len(lines), start+available)
	content := []string{title, "OFFLINE SANDBOX - no Azure calls, no files saved", ""}
	content = append(content, lines[start:end]...)
	footer := "Esc: gallery | Ctrl+P: commands | F1: help | Ctrl+C: quit"
	if len(lines) > available {
		footer = fmt.Sprintf("Lines %d-%d/%d | Ctrl+Up/Down: scroll | Esc: gallery", start+1, end, len(lines))
	}
	content = append(content, m.status, footer)
	for i := range content {
		content[i] = ansi.Truncate(content[i], max(1, m.width), "")
	}
	m.buttons = nil
	if m.demo == 1 && m.form == nil && !m.palette && !m.help {
		for y := 3; y < 3+end-start; y++ {
			line := ansi.Strip(content[y])
			for action, label := range resourceActions {
				if index := strings.Index(line, label); index >= 0 && (action == 0 || m.hasSelectedResource()) {
					m.buttons = append(m.buttons, buttonHit{
						x: lipgloss.Width(line[:index]) - 2, y: max(3, y-1),
						width: len(label) + 4, height: min(y+2, 3+end-start) - max(3, y-1), action: action,
					})
				}
			}
		}
	}
	view := tea.NewView(strings.Join(content, "\n"))
	if m.demo == 1 && m.form == nil && !m.palette && !m.help {
		view.MouseMode = tea.MouseModeCellMotion
	}
	view.AltScreen = true
	return view
}

func demosHelp(demo int) string {
	switch demo {
	case 1:
		return "Up/Down moves through the resource tree. Left/Right collapses or expands groups.\n" +
			"Enter opens a resource editor or toggles a group. Tab switches between the tree and actions.\n" +
			"Esc cancels an edit without changing the tree."
	case 2:
		return "Change form values and watch the summary. Tab advances, Shift+Tab goes back."
	case 3:
		return "Choose hosting, then advance. Only that hosting type's settings page is shown.\n" +
			"Shift+Tab returns to previous fields and pages."
	case 4:
		return "Explore region names, identifiers, and geographic labels. Enter selects a region."
	case 5:
		return "Three checks run concurrently. Locations and tenant details fail.\n" +
			"Up/Down selects a check. R retries the selected failure, A retries all failures.\n" +
			"Successful and loading checks are left alone. Enter restarts the simulation."
	case 6:
		return "Finish the form to review changes. Edit before simulating apply."
	case 7:
		return "Ctrl+P opens the palette. Type to filter, arrows to choose, Enter to open."
	case 8:
		return "Resize across 90 columns: summary moves beside the form or below it."
	default:
		return "Eight independent demos share an in-memory draft. Nothing is deployed or persisted."
	}
}
