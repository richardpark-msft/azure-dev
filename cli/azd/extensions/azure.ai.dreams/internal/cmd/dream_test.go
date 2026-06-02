// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestResolveDreamTitleDefault(t *testing.T) {
	t.Parallel()

	title := resolveDreamTitle("", "I was flying over a city at sunrise")
	if title == "" {
		t.Fatal("expected non-empty title")
	}
}

func TestResolveDreamTextValidation(t *testing.T) {
	t.Parallel()

	_, err := resolveDreamText("", "")
	if err == nil {
		t.Fatal("expected validation error when no text source is provided")
	}

	_, err = resolveDreamText("text", "file.txt")
	if err == nil {
		t.Fatal("expected validation error when both text and file are provided")
	}
}
