package copilot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
)

type ACPSession struct {
	id acp.SessionId
	conn *acp.ClientSideConnection
	logger *ACPMessageLogger
}

//
// These functions satisfy the agent.Agent interface
// (we can't reference it because it ends up being a circular reference)
// var _ agent.Agent = &ACPSession{}
//

// well...this is a lie because we're not returning any output yet, but that's not needed for what we're doing.
func (session *ACPSession) SendMessage(ctx context.Context, args ...string) (string, error) {
	// so...due to the way the ACP library seems to work, we can sometimes get output requests _after_ we've been given the notification to stop.
	// RP: I could also just split this across multiple content blocks. I don't know if there's an advantage to either method.
	prompt := strings.Join(args, "\n")

	session.logger.LogCustomMessage(prompt)

	pr, err := session.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: acp.SessionId(session.id),
		Prompt: []acp.ContentBlock{
			{
				Text: &acp.ContentBlockText{
					Text: "NOTE: NEVER run tools in parallel. Always run them sequentially, prompting the user if needed.",
				},
			},
			{
				Text: &acp.ContentBlockText{
					Text: prompt,
				},
			},
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	if pr.StopReason == acp.StopReasonEndTurn {
		time.Sleep(5 * time.Second)
		session.logger.LogCustomMessage(fmt.Sprintf("Stopping session %s because turn ended", session.id))
		return "", nil
	}

	session.logger.LogCustomMessage(fmt.Sprintf("Stopping session %s because of %v", session.id, pr.StopReason))
	return "", fmt.Errorf("failed when prompting, stop reason = %s", pr.StopReason)
}

// well...this is a lie because we're not returning any output yet, but that's not needed for what we're doing.
func (sess *ACPSession) SendMessageWithRetry(ctx context.Context, args ...string) (string, error) {
	return sess.SendMessage(ctx, args...)
}

func (sess *ACPSession) Stop() error {
	return nil
}
