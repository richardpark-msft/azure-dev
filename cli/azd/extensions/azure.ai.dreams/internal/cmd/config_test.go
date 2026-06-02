// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestConfigFromValuesRequiresStorageConnectionString(t *testing.T) {
	t.Parallel()

	_, err := configFromValues(map[string]string{}, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing storage connection string")
	}
}

func TestConfigFromValuesUsesAzdEnvironmentSettings(t *testing.T) {
	t.Parallel()

	cfg, err := configFromValues(
		map[string]string{
			"DREAM_STORAGE_CONNECTION_STRING": "UseDevelopmentStorage=true",
			"DREAM_STORAGE_CONTAINER":         "night-notes",
			"DREAM_AI_ENDPOINT":               "https://example.openai.azure.com",
			"DREAM_AI_KEY":                    "example-key",
			"DREAM_AI_DEPLOYMENT":             "gpt",
		},
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.storageContainer != "night-notes" {
		t.Fatalf("storage container = %q, want %q", cfg.storageContainer, "night-notes")
	}
	if !cfg.hasAIConfig() {
		t.Fatal("expected AI config to be complete")
	}
}
