// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type dreamStore interface {
	Save(context.Context, *dreamRecord) error
	Load(context.Context, string) (*dreamRecord, error)
	List(context.Context) ([]dreamSummary, error)
}

type blobDreamStore struct {
	client    *azblob.Client
	container string
}

func newBlobDreamStore(cfg *extensionConfig) (*blobDreamStore, error) {
	client, err := azblob.NewClientFromConnectionString(cfg.storageConnectionString, nil)
	if err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("invalid storage connection string: %v", err),
			Code:       "invalid_storage_connection_string",
			Category:   azdext.LocalErrorCategoryDependency,
			Suggestion: "Update DREAM_STORAGE_CONNECTION_STRING with a valid Azure Storage connection string.",
		}
	}

	return &blobDreamStore{
		client:    client,
		container: cfg.storageContainer,
	}, nil
}

func (s *blobDreamStore) Save(ctx context.Context, dream *dreamRecord) error {
	if err := s.ensureContainer(ctx); err != nil {
		return err
	}

	content, err := json.Marshal(dream)
	if err != nil {
		return fmt.Errorf("marshalling dream: %w", err)
	}

	_, err = s.client.UploadStream(ctx, s.container, dreamBlobName(dream.ID), bytes.NewReader(content), nil)
	if err != nil {
		return responseErrorAsServiceError(err, "uploading dream")
	}

	return nil
}

func (s *blobDreamStore) Load(ctx context.Context, id string) (*dreamRecord, error) {
	if err := s.ensureContainer(ctx); err != nil {
		return nil, err
	}

	resp, err := s.client.DownloadStream(ctx, s.container, dreamBlobName(id), nil)
	if err != nil {
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == 404 {
			return nil, &azdext.LocalError{
				Message:    fmt.Sprintf("dream %q was not found", id),
				Code:       "dream_not_found",
				Category:   azdext.LocalErrorCategoryValidation,
				Suggestion: "Run `azd ai dream list` to find available dream IDs.",
			}
		}

		return nil, responseErrorAsServiceError(err, "loading dream")
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading dream payload: %w", err)
	}

	var dream dreamRecord
	if err := json.Unmarshal(content, &dream); err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("stored dream %q has invalid JSON", id),
			Code:       "invalid_dream_payload",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Save the dream again to replace the corrupted payload.",
		}
	}

	return &dream, nil
}

func (s *blobDreamStore) List(ctx context.Context) ([]dreamSummary, error) {
	if err := s.ensureContainer(ctx); err != nil {
		return nil, err
	}

	records := []dreamSummary{}
	pager := s.client.NewListBlobsFlatPager(s.container, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, responseErrorAsServiceError(err, "listing dreams")
		}

		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil || !strings.HasSuffix(*item.Name, ".json") {
				continue
			}

			id := strings.TrimSuffix(*item.Name, ".json")
			loaded, err := s.Load(ctx, id)
			if err != nil {
				continue
			}

			records = append(records, dreamSummary{
				ID:        loaded.ID,
				Title:     loaded.Title,
				CreatedAt: loaded.CreatedAt,
			})
		}
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	return records, nil
}

func (s *blobDreamStore) ensureContainer(ctx context.Context) error {
	_, err := s.client.CreateContainer(ctx, s.container, nil)
	if err == nil {
		return nil
	}

	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && (responseErr.StatusCode == 409 || responseErr.StatusCode == 201) {
		return nil
	}

	return responseErrorAsServiceError(err, "ensuring dream container")
}

func responseErrorAsServiceError(err error, operation string) error {
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		return &azdext.ServiceError{
			Message:     fmt.Sprintf("failed %s: %s", operation, responseErr.Error()),
			ErrorCode:   responseErr.ErrorCode,
			StatusCode:  responseErr.StatusCode,
			ServiceName: "storage.azure.com",
		}
	}

	return fmt.Errorf("failed %s: %w", operation, err)
}

func dreamBlobName(id string) string {
	return fmt.Sprintf("%s.json", strings.TrimSpace(id))
}
