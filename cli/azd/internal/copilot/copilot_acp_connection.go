package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/coder/acp-go-sdk"
)

// TODO: split the connection apart a bit - the 'handler' methods don't need to be exported from
// this type, it was just simpler that way when hacking around...
type ACPConnection struct {
	stopCopilotCLI context.CancelFunc
	conn   *acp.ClientSideConnection
	logger *ACPMessageLogger
	
	updateMu sync.Mutex
}

type ACPClientOptions struct {
	CopilotCLIPath string
	CLIOptions []string

	// LogPath receives all events, written in JSONL format
	LogPath string		

	// TODO: there's a big warning about this in the connection code in the SDK about
	// this being an unstable feature. So I'm not going to enable it, for now.
	// The model to use. By default, let's the agent decide.
	// Model string
}

var _ acp.Client = (*ACPConnection)(nil)

type copilotData struct {
	Cancel context.CancelFunc
	Stdin io.WriteCloser
	Stdout io.ReadCloser
}

func launchCopilotCLI(copilotCLIPath string, additionalCLIOptions []string) (data *copilotData, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	if copilotCLIPath == "" {
		copilotCLIPath = "copilot"		// use the default copilot CLI in the path
	}

	args := []string{
		"--acp",
	}

	args = append(args, additionalCLIOptions...)

	cmd := exec.CommandContext(ctx, copilotCLIPath, args...)
	cmd.Stderr = os.Stderr

	// Set up pipes for stdio
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start agent: %w", err)
	}

	return &copilotData{
		Stdin: stdin,
		Stdout: stdout,
		Cancel: cancel,
	}, nil
}

func NewACPClient(ctx context.Context, options *ACPClientOptions) (client *ACPConnection, err error) {	
	if options == nil {
		options = &ACPClientOptions{}
	}

	var logger *ACPMessageLogger

	if options.LogPath != "" {
		logger, err = NewACPMessageLogger(options.LogPath)

		if err != nil {
			return nil, fmt.Errorf("failed to create the ACP message logger: %w", err)
		}
	}

	copilotData, err := launchCopilotCLI(options.CopilotCLIPath, options.CLIOptions)

	if err != nil {
	  return nil, fmt.Errorf("failed to launch the copilot CLI: %w", err)
	}

	client = &ACPConnection{
		stopCopilotCLI: copilotData.Cancel,
		logger: logger,
		updateMu: sync.Mutex{},
	}

	conn := acp.NewClientSideConnection(client, copilotData.Stdin, copilotData.Stdout)
	slog.SetLogLoggerLevel(slog.LevelDebug)
	conn.SetLogger(slog.Default())

	client.conn = conn
	return client, nil
}

func (a *ACPConnection) Stop() error {
	a.stopCopilotCLI()
	<-a.conn.Done()
	return nil
}

type StartSessionRequest struct {
	acp.NewSessionRequest

	// AZDPath overrides the default (our current binary's path)
	// You'd use this if you were running this code outside of 'azd'.
	AZDPath string
}

func (a *ACPConnection) NewSession(ctx context.Context, req StartSessionRequest) (*ACPSession, error) {
	mcp, err := getMCPConfigForSelf()

	if err != nil {
		return nil, err
	}

	if req.AZDPath != "" {
		mcp.Command = req.AZDPath
	}

	req.McpServers = []acp.McpServer{
		{
			Stdio: &acp.McpServerStdio{
				Args:    mcp.Args,
			 	Command: mcp.Command,
				Name:    mcp.Name,
				Env: []acp.EnvVariable{},
			},
		},
	}

	cwd, err := os.Getwd()

	if err != nil {
	  return nil, err
	}

	cwd, err = filepath.Abs(cwd)

	if err != nil {
	  return nil, err
	}

	req.Cwd = cwd

	resp, err := a.conn.NewSession(ctx, req.NewSessionRequest)

	if err != nil {
	  return nil, err
	}

	return &ACPSession{
		id: resp.SessionId,
		conn: a.conn,
		logger: a.logger,
	}, nil
}

func (a *ACPConnection) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	a.logger.LogRequestPermission(params)

	selectOptions := ux.SelectOptions{
		Message: fmt.Sprintf("Allow '%s'", *params.ToolCall.Title),
	}

	for _, opt := range params.Options {
		selectOptions.Choices = append(selectOptions.Choices, &ux.SelectChoice{
			Label: fmt.Sprintf("%s (%s)", opt.Name, opt.Kind),
		})
	}

	sel := ux.NewSelect(&selectOptions)
	choiceIdx, err := sel.Ask(ctx)

	if err != nil {
	  return acp.RequestPermissionResponse{}, err
	}

	fmt.Println("")

	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: params.Options[*choiceIdx].OptionId,
			},
		},
	}, nil
}

func (a *ACPConnection) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	
	a.logger.LogSessionUpdate(params)
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		c := u.AgentMessageChunk.Content

		if c.Text != nil {
			//a.console.Message(ctx, fmt.Sprintf
			//fmt.Print(c.Text.Text)

			// TODO: this is going to look a bit silly, since the blocks of text aren't coming in as full lines,
			// just chunks. So we're going to have a bunch of spurrious newlines. I need a version of .Message()
			// that doesn't assume a newline is needed.
			// a.console.Message(ctx, c.Text.Text)

			// if askerConsole, ok := a.console.(*input.AskerConsole); ok {
			// 	askerConsole.Print(ctx, c.Text.Text)
			// } else {

			fmt.Print(faintString(c.Text.Text))
			// }
		}
	case u.Plan != nil:
		fmt.Println("PLAN:")

		for i, entry := range u.Plan.Entries {
			fmt.Printf("[%d] %s\n", i + 1, entry.Content)
		}
	case u.ToolCall != nil:
		// https://github.com/github/copilot-cli/issues/989
		fmt.Printf("\n%s\n", FormatToolCall(u.ToolCall))		
	case u.ToolCallUpdate != nil:
		// it's difficult to properly manage a spinner since there are times when we pop up a 
		// consent 'select' menu. So for now, just not doing it.
		if u.ToolCallUpdate.Status == nil {
			// _ = spinner.Stop(ctx)
		} else {
			switch *u.ToolCallUpdate.Status {
			case acp.ToolCallStatusPending, acp.ToolCallStatusInProgress:
				// do nothing - we have a spinner running
			case acp.ToolCallStatusCompleted:
				// spinner.UpdateText(color.GreenString("✓ ") + text)
				// _ = spinner.Stop(ctx)
			case acp.ToolCallStatusFailed:
				// spinner.UpdateText(color.RedString("! ") + text)
				// _ = spinner.Stop(ctx)
			}
		}
	case u.AgentThoughtChunk != nil || u.UserMessageChunk != nil:
		// TODO: there could be interesting output here
		//a.console.Message(fmt.Sprintf("[%s]", displayUpdateKind(u)))
	}
	return nil
}

func (a *ACPConnection) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	// these are just copied from the sample, with a small addition to print the 'action' like our other tool executions.
	if !filepath.IsAbs(params.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}

 	dir := filepath.Dir(params.Path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return acp.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", params.Path, err)
	}
	
	fmt.Print(ux.BoldString("write | "))
	fmt.Println(faintString("%d bytes to %s\n", len(params.Content), params.Path))
	return acp.WriteTextFileResponse{}, nil
}

func (a *ACPConnection) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	// these are just copied from the sample, with a small addition to print the 'action' like our other tool executions.
	if !filepath.IsAbs(params.Path) {
		return acp.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", params.Path, err)
	}
	content := string(b)
	// Apply optional line/limit (1-based line index)
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = min(max(*params.Line-1, 0), len(lines))
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 {
			if start+*params.Limit < end {
				end = start + *params.Limit
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}
	// a.console.Message(ctx, fmt.Sprintf("[Client] ReadTextFile: %s (%d bytes)\n", params.Path, len(content)))
	fmt.Print(ux.BoldString("read | "))
	fmt.Println(faintString("%d bytes from %s\n", len(content), params.Path))
	return acp.ReadTextFileResponse{Content: content}, nil
}

// RP: I never got these to fire off during my testing.

// Optional/UNSTABLE terminal methods: implement as no-ops for example
func (a *ACPConnection) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	//a.console.Message(ctx, fmt.Sprintf("[Client] CreateTerminal: %v\n", params))
	return acp.CreateTerminalResponse{TerminalId: "term-1"}, nil
}

func (a *ACPConnection) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	// a.console.Message(ctx, fmt.Sprintf("[Client] TerminalOutput: %v\n", params))
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

func (a *ACPConnection) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	// a.console.Message(ctx, fmt.Sprintf("[Client] ReleaseTerminal: %v\n", params))
	return acp.ReleaseTerminalResponse{}, nil
}

func (a *ACPConnection) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	// a.console.Message(ctx, fmt.Sprintf("[Client] WaitForTerminalExit: %v\n", params))
	return acp.WaitForTerminalExitResponse{}, nil
}

// KillTerminalCommand implements acp.Client.
func (a *ACPConnection) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	// a.console.Message(ctx, fmt.Sprintf("[Client] KillTerminalCommand: %v\n", params))
	return acp.KillTerminalCommandResponse{}, nil
}

type MCP struct {
	Name    string   `json:"-"`
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Tools   []string `json:"tools"`
}

func getMCPConfigForSelf() (MCP, error) {
	exe, err := os.Executable()
	if err != nil {
		return MCP{}, fmt.Errorf("failed to determine currently executing azd path: %w", err)
	}

	// os.Executable comes with a bunch of caveats, including not being able to properly resolve symlinks...
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}

	azdAbsPath, err := filepath.Abs(resolved)
	if err != nil {
		return MCP{}, fmt.Errorf("failed to get absolute path for azd '%s': %w", resolved, err)
	}

	return MCP{
		Name:    "azd",
		Type:    "local",
		Command: azdAbsPath,
		Args:    []string{"mcp", "start"},
		Tools:   []string{"*"},
	}, nil
}

type mcpEnvelope struct {
	MCPServers map[string]MCP `json:"mcpServers"`
}

func getMCPJson(includeSelf bool, mcps []MCP) (string, error) {
	if includeSelf {
		me, err := getMCPConfigForSelf()

		if err != nil {
			return "", err
		}

		mcps = append(mcps, me)
	}

	mcpMap := map[string]MCP{}

	for _, mcp := range mcps {
		mcpMap[mcp.Name] = mcp
	}

	envelope := mcpEnvelope{
		MCPServers: mcpMap,
	}

	mcpJSONBytes, err := json.Marshal(envelope)

	if err != nil {
		return "", err
	}

	return string(mcpJSONBytes), nil
}
