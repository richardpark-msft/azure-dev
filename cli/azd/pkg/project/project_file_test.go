// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteProjectFile_ReplacesCompleteFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, writeProjectFile(path, []byte("new project"), 0o640))

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new project", string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".azure.yaml.tmp-*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestWriteProjectFile_ConcurrentWritesRemainComplete(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "azure.yaml")
	first := strings.Repeat("a", 16*1024)
	second := strings.Repeat("b", 16*1024)

	var waitGroup sync.WaitGroup
	for _, contents := range []string{first, second} {
		waitGroup.Go(func() {
			require.NoError(t, writeProjectFile(path, []byte(contents), 0o600))
		})
	}
	waitGroup.Wait()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, []string{first, second}, string(contents))
}
