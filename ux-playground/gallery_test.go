// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func update(t *testing.T, m *gallery, msg tea.Msg) {
	t.Helper()
	_, command := m.Update(msg)
	queue := []tea.Cmd{command}
	for count := 0; len(queue) > 0; count++ {
		require.Less(t, count, 200, "commands did not settle")
		command, queue = queue[0], queue[1:]
		if command == nil {
			continue
		}
		msg := command()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case cursor.BlinkMsg:
			continue
		default:
			_, command = m.Update(msg)
			queue = append(queue, command)
		}
	}
}

func focus(t *testing.T, m *gallery) {
	t.Helper()
	require.NotNil(t, m.form)
	m.form.GetFocusedField().Focus()
	update(t, m, tea.WindowSizeMsg{Width: m.width, Height: m.height})
}

func press(t *testing.T, m *gallery, code rune) {
	t.Helper()
	update(t, m, tea.KeyPressMsg{Code: code})
}

func TestGalleryMenuOpensEveryDemo(t *testing.T) {
	for i, name := range demos {
		t.Run(name, func(t *testing.T) {
			m := newGallery()
			m.updateScreen(string(rune('1' + i)))
			require.Equal(t, i+1, m.demo)
			if m.form != nil {
				focus(t, m)
			}
			require.Contains(t, ansi.Strip(m.View().Content), name)
			press(t, m, tea.KeyEscape)
			require.Zero(t, m.demo)
		})
	}
}

func TestMenuAndPaletteShowTheirOwnSelection(t *testing.T) {
	m := newGallery()
	m.updateScreen("down")
	require.Contains(t, m.menu(), "> 2. Live summary")
	update(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	press(t, m, tea.KeyDown)
	press(t, m, tea.KeyDown)
	require.Contains(t, m.body(), "> Conditional sections")
	press(t, m, tea.KeyEscape)
	require.Contains(t, m.menu(), "> 2. Live summary")
}

func TestTerminalOutputPreservesTerminalIdentityAndControls(t *testing.T) {
	reader, file, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		require.NoError(t, file.Close())
	})
	var buffer bytes.Buffer
	writer := terminalOutput{File: file, colors: &colorprofile.Writer{Forward: &buffer, Profile: colorprofile.ASCII}}
	require.Equal(t, file.Fd(), writer.Fd())
	text := "\x1b[34mName\x1b[0m\x1b[2K\r"
	_, err = writer.Write([]byte(text))
	require.NoError(t, err)
	require.Contains(t, buffer.String(), "Name")
	require.NotContains(t, buffer.String(), "\x1b[34m")
	require.Contains(t, buffer.String(), "\x1b[2K\r")
	buffer.Reset()
	_, err = writer.WriteString(text)
	require.NoError(t, err)
	require.Contains(t, buffer.String(), "Name")
	require.NotContains(t, buffer.String(), "\x1b[34m")
	require.Contains(t, buffer.String(), "\x1b[2K\r")
}

func TestResourceButtonsMouseAndKeyboard(t *testing.T) {
	m := newGallery()
	m.open(1)
	require.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode)
	require.Len(t, m.buttons, 1, "only Add should be clickable without resources")
	button := m.buttons[0]
	update(t, m, tea.MouseClickMsg{X: button.x + 2, Y: button.y + 1, Button: tea.MouseLeft})
	require.NotNil(t, m.form)
	require.Equal(t, tea.MouseModeNone, m.View().MouseMode)
	press(t, m, tea.KeyEscape)
	m.resources = []resource{{name: "web", kind: "Container App"}}
	m.View()
	require.Len(t, m.buttons, 3)
	button = m.buttons[1]
	update(t, m, tea.MouseClickMsg{X: button.x + 2, Y: button.y + 1, Button: tea.MouseLeft})
	require.NotNil(t, m.form)
	require.Equal(t, 0, m.edit)
	press(t, m, tea.KeyEscape)
	m.View()
	button = m.buttons[2]
	update(t, m, tea.MouseClickMsg{X: button.x + 2, Y: button.y + 1, Button: tea.MouseLeft})
	require.Empty(t, m.resources)

	m.actionFocus = false
	press(t, m, tea.KeyTab)
	require.True(t, m.actionFocus)
	m.action = 0
	press(t, m, tea.KeyEnter)
	require.NotNil(t, m.form)
	press(t, m, tea.KeyEscape)
	m.resources = []resource{{name: "web", kind: "Container App"}}
	m.actionFocus, m.action = true, 1
	press(t, m, tea.KeyRight)
	press(t, m, tea.KeyEnter)
	require.Empty(t, m.resources)
}

func TestResourceButtonsWrapAndIgnoreHiddenActions(t *testing.T) {
	m := newGallery()
	m.open(1)
	m.resources = []resource{{name: "web", kind: "Container App"}}
	update(t, m, tea.WindowSizeMsg{Width: 40, Height: 35})
	m.View()
	require.Len(t, m.buttons, 3)
	require.Greater(t, m.buttons[2].y, m.buttons[0].y)
	for _, button := range m.buttons {
		require.LessOrEqual(t, button.x+button.width, 40)
	}
	m.palette = true
	m.View()
	require.Empty(t, m.buttons)
	update(t, m, tea.MouseClickMsg{X: 4, Y: 10, Button: tea.MouseLeft})
	require.Nil(t, m.form)
}

func TestResourceBuilderAddEditCancelDelete(t *testing.T) {
	m := newGallery()
	m.open(1)
	m.editResource(-1)
	focus(t, m)
	update(t, m, tea.KeyPressMsg{Code: 'w', Text: "web"})
	press(t, m, tea.KeyTab)
	press(t, m, tea.KeyTab)
	press(t, m, tea.KeyEnter)
	require.Nil(t, m.form)
	require.Equal(t, []resource{{name: "web", kind: "Container App"}}, m.resources)

	m.editResource(0)
	focus(t, m)
	update(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	press(t, m, tea.KeyEscape)
	require.Equal(t, "web", m.resources[0].name)

	m.editResource(0)
	focus(t, m)
	press(t, m, tea.KeyTab)
	press(t, m, tea.KeyDown)
	press(t, m, tea.KeyTab)
	press(t, m, tea.KeyEnter)
	require.Equal(t, "Storage Account", m.resources[0].kind)
	m.updateScreen("d")
	require.Empty(t, m.resources)
}

func TestResourceTreeCollapsesAndSkipsHiddenChildren(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newGallery()
	m.resources = []resource{
		{name: "web", kind: "Container App"},
		{name: "files", kind: "Storage Account"},
		{name: "worker", kind: "Container App"},
	}
	m.open(1)
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "▾ Compute (2)")
	require.Contains(t, view, "> web (Container App)")
	require.Contains(t, view, "└")

	press(t, m, tea.KeyLeft) // Go to the Compute heading.
	press(t, m, tea.KeyLeft) // Collapse it.
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "> ▸ Compute (2)")
	require.NotContains(t, view, "web (Container App)")
	require.NotContains(t, view, "worker (Container App)")
	require.Len(t, m.buttons, 1)
	m.updateScreen("d")
	require.Len(t, m.resources, 3)

	press(t, m, tea.KeyDown)
	require.Contains(t, ansi.Strip(m.View().Content), "> ▾ Storage (1)")
	press(t, m, tea.KeyDown)
	press(t, m, tea.KeyEnter)
	require.NotNil(t, m.form)
	require.Equal(t, "files", m.draft.name)
	press(t, m, tea.KeyEscape)

	press(t, m, tea.KeyUp)
	press(t, m, tea.KeyUp)
	press(t, m, tea.KeyRight)
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "> ▾ Compute (2)")
	require.Contains(t, view, "worker (Container App)")
}

func TestLiveSummaryUpdatesBeforeSubmit(t *testing.T) {
	m := newGallery()
	m.open(2)
	focus(t, m)
	update(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Contains(t, m.summary(), m.name)
	require.Contains(t, ansi.Strip(m.View().Content), "LIVE: "+m.name)
	press(t, m, tea.KeyTab)
	press(t, m, tea.KeyDown)
	require.Equal(t, "westeurope", m.location)
	require.Contains(t, ansi.Strip(m.View().Content), "westeurope")
	require.False(t, m.review)
}

func TestConditionalSectionsFollowHosting(t *testing.T) {
	for _, host := range []string{"Container Apps", "Functions"} {
		t.Run(host, func(t *testing.T) {
			m := newGallery()
			m.host = host
			m.open(3)
			focus(t, m)
			press(t, m, tea.KeyTab)
			press(t, m, tea.KeyTab)
			press(t, m, tea.KeyTab)
			view := ansi.Strip(m.View().Content)
			if host == "Functions" {
				require.Contains(t, view, "Function runtime")
				require.NotContains(t, view, "Container image")
			} else {
				require.Contains(t, view, "Container image")
				require.NotContains(t, view, "Function runtime")
			}
		})
	}
}

func TestRichChoicesSelectRegionsWithoutQuota(t *testing.T) {
	m := newGallery()
	m.open(4)
	m.region = 2
	m.updateScreen("enter")
	require.Equal(t, "japaneast", m.location)
	require.Contains(t, m.status, "Selected japaneast")
	m.region = 1
	m.updateScreen("enter")
	require.Equal(t, "westeurope", m.location)
	require.NotContains(t, strings.ToLower(m.body()), "quota")
	require.NotContains(t, strings.ToLower(m.body()), "cores")
}

func TestLoadingFailureRetryAndStaleResults(t *testing.T) {
	m := newGallery()
	command := m.open(5)
	batch, ok := command().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 3, "independent lookups must be batched")
	generation := m.generation
	update(t, m, lookupResult{generation: generation, index: 0})
	update(t, m, lookupResult{generation: generation, index: 1, failed: true})
	require.Equal(t, "Ready", m.loads[0])
	require.Contains(t, m.loads[1], "Failed")
	require.Equal(t, "Loading...", m.loads[2])
	m.updateScreen("down")
	retry := m.updateScreen("r")
	require.Equal(t, "Loading...", m.loads[1])
	update(t, m, retry())
	require.Equal(t, "Ready", m.loads[1])
	m.open(0)
	m.open(5)
	update(t, m, lookupResult{generation: generation, index: 1, failed: true})
	require.Equal(t, "Loading...", m.loads[1])
}

func TestLoadingPanelBordersStatesAndStableRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newGallery()
	m.open(5)
	for _, width := range []int{40, 80, 120} {
		m.width = width
		loading := ansi.Strip(m.loadingPanel())
		require.Contains(t, loading, "┌")
		require.Contains(t, loading, "┘")
		require.Contains(t, loading, "|===    | Loading locations")
		require.LessOrEqual(t, lipgloss.Width(loading), width)
		m.loads = [3]string{"Ready", "Failed: simulated timeout", "Ready"}
		m.cursor = 1
		failed := ansi.Strip(m.loadingPanel())
		require.Contains(t, failed, "(✓) Done: Loaded subscription")
		require.Contains(t, failed, "(x) Failed: Loading locations")
		require.Contains(t, failed, "> (x) Failed: Loading locations")
		require.Contains(t, failed, "[R] Retry this check")
		require.Contains(t, failed, "Simulated request timeout.")
		require.Equal(t, lipgloss.Height(loading), lipgloss.Height(failed))
		m.loads[1] = "Ready"
		ready := ansi.Strip(m.loadingPanel())
		require.NotContains(t, ready, "[R]")
		require.Equal(t, lipgloss.Height(failed), lipgloss.Height(ready))
		m.loads = [3]string{"Loading...", "Loading...", "Loading..."}
	}
}

func TestRetrySelectedAndAllFailedChecks(t *testing.T) {
	m := newGallery()
	m.open(5)
	m.loads = [3]string{"Ready", "Failed: timeout", "Failed: timeout"}
	require.Nil(t, m.updateScreen("r"), "successful check must not be retried")
	m.updateScreen("down")
	retry := m.updateScreen("r")
	require.NotNil(t, retry)
	require.Equal(t, [3]string{"Ready", "Loading...", "Failed: timeout"}, m.loads)
	require.Nil(t, m.updateScreen("r"), "loading check must not be retried again")
	update(t, m, retry())
	require.Equal(t, 1, m.cursor, "completion must preserve focus")
	require.Equal(t, "Ready", m.loads[1])

	m.loads = [3]string{"Ready", "Failed: timeout", "Failed: timeout"}
	all := m.updateScreen("a")
	batch, ok := all().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)
	require.Equal(t, [3]string{"Ready", "Loading...", "Loading..."}, m.loads)
	for _, command := range batch {
		update(t, m, command())
	}
	require.Equal(t, [3]string{"Ready", "Ready", "Ready"}, m.loads)
	require.Nil(t, m.updateScreen("a"))
}

func TestEveryDemoHasBordersAtDifferentSizes(t *testing.T) {
	for i, name := range demos {
		t.Run(name, func(t *testing.T) {
			m := newGallery()
			m.open(i + 1)
			if m.form != nil {
				focus(t, m)
			}
			for _, width := range []int{40, 80, 120} {
				update(t, m, tea.WindowSizeMsg{Width: width, Height: 35})
				view := ansi.Strip(m.View().Content)
				require.Contains(t, view, "┌")
				require.Contains(t, view, "┘")
				require.LessOrEqual(t, lipgloss.Width(view), width)
				require.LessOrEqual(t, lipgloss.Height(view), 35)
			}
		})
	}
}

func TestReviewEditAndSimulatedApply(t *testing.T) {
	m := newGallery()
	m.open(6)
	focus(t, m)
	for range 3 {
		press(t, m, tea.KeyTab)
	}
	press(t, m, tea.KeyEnter)
	require.Nil(t, m.form)
	require.True(t, m.review)
	require.False(t, m.applied)
	require.Contains(t, m.body(), "REVIEW BEFORE APPLY")
	m.updateScreen("a")
	require.True(t, m.applied)
	require.Contains(t, m.status, "No files written")
	m.updateScreen("e")
	require.NotNil(t, m.form)
	require.False(t, m.applied)
}

func TestCommandPaletteSearchAndCancelPreserveSelection(t *testing.T) {
	m := newGallery()
	m.open(1)
	m.resources = []resource{{name: "web", kind: "Container App"}}
	update(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for range 5 {
		press(t, m, tea.KeyDown)
	}
	press(t, m, tea.KeyEscape)
	require.Zero(t, m.cursor)
	update(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	update(t, m, tea.KeyPressMsg{Code: 'r', Text: "responsive"})
	require.Equal(t, []int{8}, m.paletteMatches())
	press(t, m, tea.KeyEnter)
	require.Equal(t, 8, m.demo)
	require.False(t, m.palette)
}

func TestResponsiveLayoutAndScreenBounds(t *testing.T) {
	m := newGallery()
	m.open(8)
	focus(t, m)
	for _, size := range [][2]int{{120, 35}, {80, 24}, {40, 16}, {100, 24}} {
		update(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		require.LessOrEqual(t, lipgloss.Width(view), size[0])
		require.LessOrEqual(t, lipgloss.Height(view), size[1])
		body := ansi.Strip(m.body())
		if size[0] >= 90 {
			require.Contains(t, body, "LIVE DRAFT")
			for line := range strings.SplitSeq(body, "\n") {
				if strings.Contains(line, "LIVE DRAFT") {
					require.Greater(t, strings.Index(line, "LIVE DRAFT"), 30, "summary must be beside the form")
				}
			}
		} else {
			require.Contains(t, body, "LIVE: ")
		}
	}
}

func TestHelpDoesNotEditDraft(t *testing.T) {
	m := newGallery()
	m.open(2)
	focus(t, m)
	press(t, m, tea.KeyF1)
	update(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Equal(t, "playground", m.name)
	press(t, m, tea.KeyEscape)
	require.False(t, m.help)
	require.Equal(t, 2, m.demo)
}
