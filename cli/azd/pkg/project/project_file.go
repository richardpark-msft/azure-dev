// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// writeProjectFile persists one complete azure.yaml document.
// It writes and syncs a temporary sibling before atomically replacing the
// destination so readers never observe a partially written project file.
func writeProjectFile(path string, contents []byte, permissions os.FileMode) error {
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".azure.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary project file: %w", err)
	}
	tempPath := tempFile.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tempFile.Close()
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(contents); err != nil {
		return fmt.Errorf("writing temporary project file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("syncing temporary project file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing temporary project file: %w", err)
	}
	//nolint:gosec // tempPath is created above in the destination directory.
	if err := os.Chmod(tempPath, permissions); err != nil {
		return fmt.Errorf("setting temporary project file permissions: %w", err)
	}
	if err := osutil.Rename(context.Background(), tempPath, path); err != nil {
		return fmt.Errorf("replacing project file: %w", err)
	}

	committed = true
	return nil
}
