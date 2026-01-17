package copilot

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/coder/acp-go-sdk"
	"github.com/fatih/color"
)

// ToolRawInput contains any extracted fields that we could get from the RawInput blob
// passed in various session update/tool call messages.
type ToolRawInput struct {
	// execute
	CommandLine  string
	Shells []string

	// edit
	Diff     string // this can be a large value - we probably don't want to print it.
	FileName string // 'locations' is also filled out, that's the more portable choice.

	// fetch
	URL string

	// glob
	Pattern string		// ie: *.txt

	// think
	TODOs string		// a markdown TODO list
}

func ExtractToolRawInput(rawInputAny any) (ToolRawInput) {
	rawInputMap, ok := rawInputAny.(map[string]any) 

	if !ok {
		return ToolRawInput{}
	}

	rawInput := ToolRawInput{}

	// write
	{
		if fileName, ok := rawInputMap["fileName"].(string); ok {
	        rawInput.FileName = fileName
    	}

		// TODO: this is actually a really big field, I don't think I'll ever print it out.
		if diff, ok := rawInputMap["diff"].(string); ok {
			rawInput.Diff = diff
		}
	}

	// exec
	{
		if cmd, ok := rawInputMap["command"].(string); ok {
			rawInput.CommandLine = cmd
		}

		if cmds, ok := rawInputMap["commands"].([]any); ok {
			for _, c := range cmds {
				if s, ok := c.(string); ok {
					rawInput.Shells = append(rawInput.Shells, s)
				}
			}
		}
	}

	// fetch
	if url, ok := rawInputMap["url"].(string); ok {
		rawInput.URL = url
	}

	// glob
	if pattern, ok := rawInputMap["pattern"].(string); ok {
		rawInput.Pattern = pattern
	}

	// read
	// (the value here overlaps with location, which is a public field, so we'll ignore it)

	// think
	if todosText, ok := rawInputMap["todos"].(string); ok {
		rawInput.TODOs = todosText
	}

	return rawInput
}

func FormatLocations(locations []acp.ToolCallLocation,indent string) string {
		var paths []string

		for _, location := range locations {
			paths = append(paths, location.Path)
		}

		return strings.Join(paths, ",")
}


var faintString = color.New(color.Faint).SprintfFunc()

func FormatToolCall[ToolT acp.SessionToolCallUpdate|acp.RequestPermissionToolCall|acp.SessionUpdateToolCall](v *ToolT) string {
	var kind *acp.ToolKind
	var title *string
	var locations []acp.ToolCallLocation
	var rawInputAny any

	switch tc := any(*v).(type) {
	case acp.SessionUpdateToolCall:		
	kind, title, locations, rawInputAny = &tc.Kind, &tc.Title, tc.Locations, tc.RawInput
	case acp.SessionToolCallUpdate:
		kind, title, locations, rawInputAny = tc.Kind, tc.Title, tc.Locations, tc.RawInput
	case acp.RequestPermissionToolCall:
		kind, title, locations, rawInputAny = tc.Kind, tc.Title, tc.Locations, tc.RawInput
	default:
		panic(fmt.Sprintf("Unhandled type passed to FormatToolCall: %T", v))
	}

	rawInput := ExtractToolRawInput(rawInputAny)

	var subTitle string

	sb := strings.Builder{}
	sb.WriteString(ux.BoldString(*title))
	sb.WriteString(" | ")
	
	switch *kind {
	case acp.ToolKindExecute:
        // bash is just executing commands, so the 'command' is just a normal command line.
		if slices.Contains(rawInput.Shells, "bash") {
			subTitle = "> " + rawInput.CommandLine
		} else {
			// on Windows (which is where we hit this branch the most), the command is usually Powershell syntax
			// so we'll print out the 'commmands' part as well (ie: the shell)
			subTitle = fmt.Sprintf("> %s %s", strings.Join(rawInput.Shells, ","), rawInput.CommandLine)
		}
	case acp.ToolKindEdit, acp.ToolKindRead:
		subTitle = FormatLocations(locations, "   ") + rawInput.Pattern
	case acp.ToolKindFetch:
		subTitle = ux.Hyperlink(rawInput.URL)
	case acp.ToolKindThink:
		if title != nil  && *title == "update_todo" {
			subTitle = fmt.Sprintf("\n%s\n", strings.TrimSpace(rawInput.TODOs))
			break
		}

		subTitle = fmt.Sprintf("%#v", rawInputAny)
	default:
		subTitle = fmt.Sprintf("%#v", rawInputAny)
	}

	sb.WriteString(faintString(subTitle))
	return sb.String()
}

