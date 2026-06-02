// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type dreamInterpreter interface {
	Interpret(context.Context, string) (*dreamInterpretation, error)
}

type azureOpenAIInterpreter struct {
	httpClient *http.Client
	endpoint   string
	apiKey     string
	deployment string
	apiVersion string
}

func newAzureOpenAIInterpreter(cfg *extensionConfig) (*azureOpenAIInterpreter, error) {
	if !cfg.hasAIConfig() {
		return nil, &azdext.LocalError{
			Message:  "AI interpretation is not configured",
			Code:     "missing_ai_configuration",
			Category: azdext.LocalErrorCategoryDependency,
			Suggestion: "Set DREAM_AI_ENDPOINT, DREAM_AI_KEY, and DREAM_AI_DEPLOYMENT " +
				"in the active azd environment or shell.",
		}
	}

	return &azureOpenAIInterpreter{
		httpClient: &http.Client{},
		endpoint:   strings.TrimRight(cfg.aiEndpoint, "/"),
		apiKey:     cfg.aiKey,
		deployment: cfg.aiDeployment,
		apiVersion: cfg.aiAPIVersion,
	}, nil
}

func (i *azureOpenAIInterpreter) Interpret(ctx context.Context, dreamText string) (*dreamInterpretation, error) {
	if len([]rune(dreamText)) > maxDreamChars {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("dream text exceeds %d characters", maxDreamChars),
			Code:       "dream_text_too_long",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Shorten the dream text before requesting interpretation.",
		}
	}

	apiURL := fmt.Sprintf(
		"%s/openai/deployments/%s/chat/completions?api-version=%s",
		i.endpoint,
		url.PathEscape(i.deployment),
		url.QueryEscape(i.apiVersion),
	)

	payload := map[string]any{
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You interpret dreams. Return only JSON with keys " +
					"summary (string), sentiment (string), themes (string[]), symbols (string[]).",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Interpret this dream:\n%s", dreamText),
			},
		},
		"temperature": 0.2,
		"max_tokens":  350,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling AI payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating AI request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", i.apiKey)

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, &azdext.ServiceError{
			Message:     fmt.Sprintf("failed calling AI endpoint: %v", err),
			ServiceName: "openai.azure.com",
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("reading AI response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorCode := "ai_request_failed"
		var errorPayload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errorPayload) == nil && errorPayload.Error.Code != "" {
			errorCode = errorPayload.Error.Code
		}

		return nil, &azdext.ServiceError{
			Message:     fmt.Sprintf("AI service returned %d", resp.StatusCode),
			ErrorCode:   errorCode,
			StatusCode:  resp.StatusCode,
			ServiceName: "openai.azure.com",
		}
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &completion); err != nil {
		return nil, fmt.Errorf("parsing AI completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, &azdext.LocalError{
			Message:    "AI response contained no choices",
			Code:       "empty_ai_response",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Retry the interpretation request.",
		}
	}

	result := parseInterpretationPayload(completion.Choices[0].Message.Content)
	return &result, nil
}

func parseInterpretationPayload(content string) dreamInterpretation {
	raw := strings.TrimSpace(content)
	candidate := strings.TrimSpace(raw)
	if strings.HasPrefix(candidate, "```") {
		lines := strings.Split(candidate, "\n")
		if len(lines) >= 3 {
			candidate = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	result := dreamInterpretation{
		Raw: raw,
	}

	var parsed struct {
		Summary   string   `json:"summary"`
		Sentiment string   `json:"sentiment"`
		Themes    []string `json:"themes"`
		Symbols   []string `json:"symbols"`
	}

	if json.Unmarshal([]byte(candidate), &parsed) == nil {
		result.Summary = strings.TrimSpace(parsed.Summary)
		result.Sentiment = strings.TrimSpace(parsed.Sentiment)
		result.Themes = parsed.Themes
		result.Symbols = parsed.Symbols
	}

	return result
}
