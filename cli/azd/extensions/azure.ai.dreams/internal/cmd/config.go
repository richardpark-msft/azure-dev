// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type extensionConfig struct {
	storageConnectionString string
	storageContainer        string
	aiEndpoint              string
	aiKey                   string
	aiDeployment            string
	aiAPIVersion            string
}

func loadConfig(
	ctx context.Context,
	extCtx *azdext.ExtensionContext,
	azdClient *azdext.AzdClient,
) (*extensionConfig, error) {
	envValues := map[string]string{}
	envName := extCtx.Environment
	if envName == "" {
		if currentEnv, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			envName = currentEnv.Environment.Name
		}
	}

	if envName != "" {
		keys := []string{
			"DREAM_STORAGE_CONNECTION_STRING",
			"AZURE_STORAGE_CONNECTION_STRING",
			"DREAM_STORAGE_CONTAINER",
			"DREAM_AI_ENDPOINT",
			"DREAM_AI_KEY",
			"DREAM_AI_DEPLOYMENT",
			"DREAM_AI_API_VERSION",
		}

		for _, key := range keys {
			valueResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envName,
				Key:     key,
			})
			if err == nil {
				envValues[key] = strings.TrimSpace(valueResp.Value)
			}
		}
	}

	cfg, err := configFromValues(envValues, map[string]string{
		"DREAM_STORAGE_CONNECTION_STRING": strings.TrimSpace(os.Getenv("DREAM_STORAGE_CONNECTION_STRING")),
		"AZURE_STORAGE_CONNECTION_STRING": strings.TrimSpace(os.Getenv("AZURE_STORAGE_CONNECTION_STRING")),
		"DREAM_STORAGE_CONTAINER":         strings.TrimSpace(os.Getenv("DREAM_STORAGE_CONTAINER")),
		"DREAM_AI_ENDPOINT":               strings.TrimSpace(os.Getenv("DREAM_AI_ENDPOINT")),
		"DREAM_AI_KEY":                    strings.TrimSpace(os.Getenv("DREAM_AI_KEY")),
		"DREAM_AI_DEPLOYMENT":             strings.TrimSpace(os.Getenv("DREAM_AI_DEPLOYMENT")),
		"DREAM_AI_API_VERSION":            strings.TrimSpace(os.Getenv("DREAM_AI_API_VERSION")),
	})
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func configFromValues(azdValues map[string]string, osValues map[string]string) (*extensionConfig, error) {
	cfg := &extensionConfig{
		storageConnectionString: firstNonEmpty(
			azdValues["DREAM_STORAGE_CONNECTION_STRING"],
			azdValues["AZURE_STORAGE_CONNECTION_STRING"],
			osValues["DREAM_STORAGE_CONNECTION_STRING"],
			osValues["AZURE_STORAGE_CONNECTION_STRING"],
		),
		storageContainer: firstNonEmpty(
			azdValues["DREAM_STORAGE_CONTAINER"],
			osValues["DREAM_STORAGE_CONTAINER"],
			defaultContainer,
		),
		aiEndpoint: firstNonEmpty(
			azdValues["DREAM_AI_ENDPOINT"],
			osValues["DREAM_AI_ENDPOINT"],
		),
		aiKey: firstNonEmpty(
			azdValues["DREAM_AI_KEY"],
			osValues["DREAM_AI_KEY"],
		),
		aiDeployment: firstNonEmpty(
			azdValues["DREAM_AI_DEPLOYMENT"],
			osValues["DREAM_AI_DEPLOYMENT"],
		),
		aiAPIVersion: firstNonEmpty(
			azdValues["DREAM_AI_API_VERSION"],
			osValues["DREAM_AI_API_VERSION"],
			defaultAIApiVersion,
		),
	}

	if cfg.storageConnectionString == "" {
		return nil, &azdext.LocalError{
			Message: "missing Azure Storage connection string",
			Code:    "missing_storage_connection_string",
			Category: azdext.LocalErrorCategoryDependency,
			Suggestion: "Set DREAM_STORAGE_CONNECTION_STRING (or AZURE_STORAGE_CONNECTION_STRING) " +
				"in the active azd environment or shell.",
		}
	}

	return cfg, nil
}

func (c *extensionConfig) hasAIConfig() bool {
	return c.aiEndpoint != "" && c.aiKey != "" && c.aiDeployment != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
