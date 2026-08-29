// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListLayers_ResolvesInfraServicesAndDependencies(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"ai-project": {
				Name:  "ai-project",
				Layer: "foundry",
			},
			"writer-agent": {
				Name:  "writer-agent",
				Layer: "writer-agent",
				Uses:  []string{"ai-project"},
			},
			"legacy": {
				Name: "legacy",
			},
		},
		Infra: provisioning.Options{Layers: []provisioning.Options{
			{
				Name:     "foundry",
				Provider: "microsoft.foundry",
				Outputs:  map[string]string{"AZURE_AI_PROJECT_ID": "FOUNDRY_PROJECT_ID"},
			},
		}},
	}

	layers, err := NewImportManager(nil).ListLayers(t.Context(), projectConfig)

	require.NoError(t, err)
	require.Len(t, layers, 3)
	assert.Equal(t, "", layers[0].Name)
	assert.True(t, layers[0].Implicit)
	assert.Nil(t, layers[0].Infra)
	require.Len(t, layers[0].Services, 1)
	assert.Equal(t, "legacy", layers[0].Services[0].Name)

	assert.Equal(t, "foundry", layers[1].Name)
	require.NotNil(t, layers[1].Infra)
	assert.Equal(t, provisioning.ProviderKind("microsoft.foundry"), layers[1].Infra.Provider)
	assert.Equal(t, map[string]string{"AZURE_AI_PROJECT_ID": "FOUNDRY_PROJECT_ID"}, layers[1].Outputs)

	assert.Equal(t, "writer-agent", layers[2].Name)
	assert.Nil(t, layers[2].Infra)
	assert.Equal(t, []string{"foundry"}, layers[2].DependsOn)
}

func TestListLayers_UnionsExplicitAndDerivedDependencies(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"api":      {Name: "api", Layer: "application", Uses: []string{"database"}},
			"database": {Name: "database", Layer: "data"},
		},
		Infra: provisioning.Options{Layers: []provisioning.Options{
			{Name: "network", Provider: provisioning.Bicep},
			{Name: "data", Provider: provisioning.Bicep, DependsOn: []string{"network"}},
		}},
	}

	layer, err := NewImportManager(nil).GetLayer(t.Context(), projectConfig, "application")

	require.NoError(t, err)
	assert.Equal(t, []string{"data"}, layer.DependsOn)

	data, err := NewImportManager(nil).GetLayer(t.Context(), projectConfig, "data")
	require.NoError(t, err)
	assert.Equal(t, []string{"network"}, data.DependsOn)
}

func TestListLayers_ServiceDependsOnLayer(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"writer": {
				Name:      "writer",
				Layer:     "agents",
				DependsOn: []string{"foundry"},
			},
			"ai-project": {Name: "ai-project", Layer: "foundry"},
		},
	}

	layer, err := NewImportManager(nil).GetLayer(t.Context(), projectConfig, "agents")

	require.NoError(t, err)
	assert.Equal(t, []string{"foundry"}, layer.DependsOn)
}

func TestListLayers_ServiceDependsOnUnknownLayer(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Services: map[string]*ServiceConfig{
		"writer": {Name: "writer", Layer: "agents", DependsOn: []string{"missing"}},
	}}

	_, err := NewImportManager(nil).ListLayers(t.Context(), projectConfig)

	require.ErrorContains(t, err, "depends on unknown layer")
}

func TestListLayers_ServiceDependsOnOwnLayer(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Services: map[string]*ServiceConfig{
		"writer": {Name: "writer", Layer: "agents", DependsOn: []string{"agents"}},
	}}

	_, err := NewImportManager(nil).ListLayers(t.Context(), projectConfig)

	require.ErrorContains(t, err, "cannot depend on its own layer")
}

func TestListLayers_RejectsLayerCycle(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Infra: provisioning.Options{Layers: []provisioning.Options{
		{Name: "a", DependsOn: []string{"b"}},
		{Name: "b", DependsOn: []string{"a"}},
	}}}

	_, err := NewImportManager(nil).ListLayers(t.Context(), projectConfig)

	require.ErrorContains(t, err, "circular dependency")
}

func TestListLayers_RejectsDuplicateOutputOwner(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Infra: provisioning.Options{Layers: []provisioning.Options{
		{Name: "a", Outputs: map[string]string{"OUTPUT_A": "SHARED"}},
		{Name: "b", Outputs: map[string]string{"OUTPUT_B": "SHARED"}},
	}}}

	_, err := NewImportManager(nil).ListLayers(t.Context(), projectConfig)

	require.ErrorContains(t, err, "owned by both layers")
}

func TestGetLayer_NotFound(t *testing.T) {
	t.Parallel()

	_, err := NewImportManager(nil).GetLayer(t.Context(), &ProjectConfig{}, "missing")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLayerNotFound))
}

func TestSelectLayers(t *testing.T) {
	t.Parallel()

	layers := []*Layer{
		{Name: "foundry"},
		{Name: "tools", DependsOn: []string{"foundry"}},
		{Name: "agent", DependsOn: []string{"tools"}},
	}

	exact, err := SelectLayers(layers, "agent", false)
	require.NoError(t, err)
	require.Len(t, exact, 1)
	assert.Equal(t, "agent", exact[0].Name)

	closure, err := SelectLayers(layers, "agent", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"foundry", "tools", "agent"}, []string{
		closure[0].Name,
		closure[1].Name,
		closure[2].Name,
	})
}
