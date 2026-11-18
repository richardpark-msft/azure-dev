// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

func main() {
	writer := terminalOutput{File: os.Stdout, colors: colorprofile.NewWriter(os.Stdout, os.Environ())}
	if _, err := tea.NewProgram(newGallery(), tea.WithOutput(writer)).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "UX playground: %v\n", err)
		os.Exit(1)
	}
}

// terminalOutput preserves terminal detection while honoring NO_COLOR.
type terminalOutput struct {
	*os.File
	colors *colorprofile.Writer
}

func (w terminalOutput) Write(data []byte) (int, error) {
	return w.colors.Write(data)
}

func (w terminalOutput) WriteString(text string) (int, error) {
	return w.Write([]byte(text))
}
