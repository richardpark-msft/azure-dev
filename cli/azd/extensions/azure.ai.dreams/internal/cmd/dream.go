// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newDreamSaveCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var title string
	var text string
	var file string

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save a dream to Azure Storage.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := extCtx.Context()
			store, err := newStoreFromContext(ctx, extCtx)
			if err != nil {
				return err
			}

			dreamText, err := resolveDreamText(text, file)
			if err != nil {
				return err
			}

			if len([]rune(dreamText)) > maxDreamChars {
				return &azdext.LocalError{
					Message:    fmt.Sprintf("dream text exceeds %d characters", maxDreamChars),
					Code:       "dream_text_too_long",
					Category:   azdext.LocalErrorCategoryValidation,
					Suggestion: "Shorten the dream text before saving.",
				}
			}

			record := &dreamRecord{
				ID:        uuid.NewString(),
				Title:     resolveDreamTitle(title, dreamText),
				Text:      dreamText,
				CreatedAt: time.Now().UTC(),
			}

			if err := store.Save(ctx, record); err != nil {
				return err
			}

			return printOutput(extCtx.OutputFormat, map[string]any{
				"saved": true,
				"dream": record,
			}, fmt.Sprintf("Saved dream %s", record.ID))
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Title for the dream entry")
	cmd.Flags().StringVar(&text, "text", "", "Dream text")
	cmd.Flags().StringVar(&file, "file", "", "Path to a file containing dream text")

	return cmd
}

func newDreamListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved dreams.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := extCtx.Context()
			store, err := newStoreFromContext(ctx, extCtx)
			if err != nil {
				return err
			}

			records, err := store.List(ctx)
			if err != nil {
				return err
			}

			if isJSONOutput(extCtx.OutputFormat) {
				return printOutput(extCtx.OutputFormat, records, "")
			}

			if len(records) == 0 {
				fmt.Println("No dreams found.")
				return nil
			}

			for _, dream := range records {
				fmt.Printf("%s\t%s\t%s\n", dream.ID, dream.CreatedAt.Format(time.RFC3339), dream.Title)
			}
			return nil
		},
	}
}

func newDreamLoadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:   "load",
		Short: "Load a saved dream by ID.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := extCtx.Context()
			store, err := newStoreFromContext(ctx, extCtx)
			if err != nil {
				return err
			}

			record, err := store.Load(ctx, id)
			if err != nil {
				return err
			}

			return printOutput(extCtx.OutputFormat, record, record.Text)
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Dream ID to load")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newDreamInterpretCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var id string
	var text string
	var save bool

	cmd := &cobra.Command{
		Use:   "interpret",
		Short: "Interpret a dream with Azure AI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := extCtx.Context()
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("creating azd client: %w", err)
			}
			defer azdClient.Close()

			cfg, err := loadConfig(ctx, extCtx, azdClient)
			if err != nil {
				return err
			}

			interpreter, err := newAzureOpenAIInterpreter(cfg)
			if err != nil {
				return err
			}

			dreamText := strings.TrimSpace(text)
			recordID := ""
			var dream *dreamRecord
			var store dreamStore

			if id != "" {
				store, err = newBlobDreamStore(cfg)
				if err != nil {
					return err
				}

				dream, err = store.Load(ctx, id)
				if err != nil {
					return err
				}

				recordID = dream.ID
				dreamText = dream.Text
			}

			if strings.TrimSpace(dreamText) == "" {
				return &azdext.LocalError{
					Message:    "provide --text or --id for interpretation",
					Code:       "missing_interpretation_input",
					Category:   azdext.LocalErrorCategoryValidation,
					Suggestion: "Pass --text for ad hoc analysis or --id to interpret a saved dream.",
				}
			}

			interpretation, err := interpreter.Interpret(ctx, dreamText)
			if err != nil {
				return err
			}

			if dream != nil && save {
				dream.Interpretation = interpretation
				if err := store.Save(ctx, dream); err != nil {
					return err
				}
			}

			result := interpretResult{
				DreamID:          recordID,
				Interpretation:   *interpretation,
				InterpretationAt: time.Now().UTC(),
			}

			return printOutput(extCtx.OutputFormat, result, interpretation.Raw)
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Saved dream ID to interpret")
	cmd.Flags().StringVar(&text, "text", "", "Dream text to interpret directly")
	cmd.Flags().BoolVar(&save, "save", true, "Persist interpretation back to storage when --id is used")

	return cmd
}

func newStoreFromContext(ctx context.Context, extCtx *azdext.ExtensionContext) (dreamStore, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("creating azd client: %w", err)
	}
	defer azdClient.Close()

	cfg, err := loadConfig(ctx, extCtx, azdClient)
	if err != nil {
		return nil, err
	}

	return newBlobDreamStore(cfg)
}

func resolveDreamText(text string, file string) (string, error) {
	trimmedText := strings.TrimSpace(text)
	trimmedFile := strings.TrimSpace(file)

	if trimmedText == "" && trimmedFile == "" {
		return "", &azdext.LocalError{
			Message:    "provide --text or --file",
			Code:       "missing_dream_text",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Use --text for inline entry or --file to read from a file.",
		}
	}

	if trimmedText != "" && trimmedFile != "" {
		return "", &azdext.LocalError{
			Message:    "use either --text or --file, not both",
			Code:       "conflicting_dream_text_inputs",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Provide only one dream input source.",
		}
	}

	if trimmedText != "" {
		return trimmedText, nil
	}

	content, err := os.ReadFile(trimmedFile)
	if err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("failed reading %q: %v", trimmedFile, err),
			Code:       "dream_file_read_failed",
			Category:   azdext.LocalErrorCategoryDependency,
			Suggestion: "Verify the file path and permissions, then try again.",
		}
	}

	result := strings.TrimSpace(string(content))
	if result == "" {
		return "", &azdext.LocalError{
			Message:    "dream file is empty",
			Code:       "empty_dream_file",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Add dream content to the file and retry.",
		}
	}

	return result, nil
}

func resolveDreamTitle(title, text string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}

	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	if len(runes) == 0 {
		return "Untitled dream"
	}
	return string(runes)
}

func printOutput(outputFormat string, data any, textFallback string) error {
	if isJSONOutput(outputFormat) {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)
	}

	if textFallback != "" {
		fmt.Println(textFallback)
		return nil
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func isJSONOutput(outputFormat string) bool {
	return strings.EqualFold(strings.TrimSpace(outputFormat), "json")
}
