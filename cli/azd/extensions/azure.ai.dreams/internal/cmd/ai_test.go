// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestParseInterpretationPayloadJSON(t *testing.T) {
	t.Parallel()

	result := parseInterpretationPayload(`{"summary":"test","sentiment":"positive","themes":["growth"],"symbols":["river"]}`)
	if result.Summary != "test" {
		t.Fatalf("summary = %q, want %q", result.Summary, "test")
	}
	if result.Sentiment != "positive" {
		t.Fatalf("sentiment = %q, want %q", result.Sentiment, "positive")
	}
	if len(result.Themes) != 1 || result.Themes[0] != "growth" {
		t.Fatalf("themes = %#v", result.Themes)
	}
}

func TestParseInterpretationPayloadMarkdownFence(t *testing.T) {
	t.Parallel()

	result := parseInterpretationPayload("```json\n{\"summary\":\"fenced\"}\n```")
	if result.Summary != "fenced" {
		t.Fatalf("summary = %q, want %q", result.Summary, "fenced")
	}
}

func TestParseInterpretationPayloadInvalidJSON(t *testing.T) {
	t.Parallel()

	raw := "not json"
	result := parseInterpretationPayload(raw)
	if result.Raw != raw {
		t.Fatalf("raw = %q, want %q", result.Raw, raw)
	}
	if result.Summary != "" {
		t.Fatalf("summary = %q, want empty", result.Summary)
	}
}
