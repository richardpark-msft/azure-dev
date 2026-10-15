// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

// ------------------------------------------------------------------
// toCiProviderType
// ------------------------------------------------------------------

func Test_toCiProviderType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     ciProviderType
		wantErr  bool
		errMatch string
	}{
		{
			name:  "github",
			input: "github",
			want:  ciProviderGitHubActions,
		},
		{
			name:  "azdo",
			input: "azdo",
			want:  ciProviderAzureDevOps,
		},
		{
			name:     "invalid",
			input:    "jenkins",
			wantErr:  true,
			errMatch: "invalid ci provider type jenkins",
		},
		{
			name:     "empty string",
			input:    "",
			wantErr:  true,
			errMatch: "invalid ci provider type",
		},
		{
			name:     "mixed case is invalid",
			input:    "GitHub",
			wantErr:  true,
			errMatch: "invalid ci provider type GitHub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toCiProviderType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMatch)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ------------------------------------------------------------------
// toInfraProviderType
// ------------------------------------------------------------------

func Test_toInfraProviderType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     infraProviderType
		wantErr  bool
		errMatch string
	}{
		{
			name:  "bicep",
			input: "bicep",
			want:  infraProviderBicep,
		},
		{
			name:  "terraform",
			input: "terraform",
			want:  infraProviderTerraform,
		},
		{
			name:  "empty is valid (undefined)",
			input: "",
			want:  infraProviderUndefined,
		},
		{
			name:     "invalid provider",
			input:    "pulumi",
			wantErr:  true,
			errMatch: "invalid infra provider type pulumi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInfraProviderType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMatch)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ------------------------------------------------------------------
// generateFilePaths
// ------------------------------------------------------------------

func Test_generateFilePaths(t *testing.T) {
	tests := []struct {
		name  string
		dirs  []string
		files []string
		want  []string
	}{
		{
			name:  "single dir single file",
			dirs:  []string{".github/workflows"},
			files: []string{"azure-dev.yml"},
			want: []string{
				filepath.Join(".github/workflows", "azure-dev.yml"),
			},
		},
		{
			name:  "multiple dirs multiple files",
			dirs:  []string{".azdo/pipelines", ".azuredevops/pipelines"},
			files: []string{"azure-dev.yml", "azure-dev.yaml"},
			want: []string{
				filepath.Join(".azdo/pipelines", "azure-dev.yml"),
				filepath.Join(".azdo/pipelines", "azure-dev.yaml"),
				filepath.Join(".azuredevops/pipelines", "azure-dev.yml"),
				filepath.Join(".azuredevops/pipelines", "azure-dev.yaml"),
			},
		},
		{
			name:  "empty dirs returns nil",
			dirs:  []string{},
			files: []string{"file.yml"},
			want:  nil,
		},
		{
			name:  "empty files returns nil",
			dirs:  []string{".github"},
			files: []string{},
			want:  nil,
		},
		{
			name:  "both empty returns nil",
			dirs:  []string{},
			files: []string{},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateFilePaths(tt.dirs, tt.files)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ------------------------------------------------------------------
// hasPipelineFile
// ------------------------------------------------------------------

func Test_hasPipelineFile(t *testing.T) {
	t.Run("returns true when pipeline file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		ghDir := filepath.Join(
			tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(ghDir, "azure-dev.yml"),
			[]byte("trigger: none"), 0600))

		assert.True(t, hasPipelineFile(ciProviderGitHubActions, tmpDir))
	})

	t.Run("returns false when no pipeline file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		assert.False(t,
			hasPipelineFile(ciProviderGitHubActions, tmpDir))
	})

	t.Run("returns true for azdo provider", func(t *testing.T) {
		tmpDir := t.TempDir()
		azdoDir := filepath.Join(tmpDir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(azdoDir, "azure-dev.yml"),
			[]byte("trigger: none"), 0600))

		assert.True(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
	})

	t.Run("returns true for azdo alt dir", func(t *testing.T) {
		tmpDir := t.TempDir()
		altDir := filepath.Join(
			tmpDir, ".azuredevops", "pipelines")
		require.NoError(t, os.MkdirAll(altDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(altDir, "azure-dev.yaml"),
			[]byte("trigger: none"), 0600))

		assert.True(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
	})
}

// ------------------------------------------------------------------
// resolveSmr
// ------------------------------------------------------------------

func Test_resolveSmr(t *testing.T) {
	t.Run("returns arg when provided", func(t *testing.T) {
		result := resolveSmr(
			"arg-value",
			config.NewEmptyConfig(),
			config.NewEmptyConfig())
		require.NotNil(t, result)
		assert.Equal(t, "arg-value", *result)
	})

	t.Run("returns project config value", func(t *testing.T) {
		projCfg := config.NewConfig(nil)
		_ = projCfg.Set(
			"pipeline.config.applicationServiceManagementReference",
			"proj-smr")

		result := resolveSmr("", projCfg, config.NewEmptyConfig())
		require.NotNil(t, result)
		assert.Equal(t, "proj-smr", *result)
	})

	t.Run("returns user config when project empty", func(t *testing.T) {
		userCfg := config.NewConfig(nil)
		_ = userCfg.Set(
			"pipeline.config.applicationServiceManagementReference",
			"user-smr")

		result := resolveSmr(
			"", config.NewEmptyConfig(), userCfg)
		require.NotNil(t, result)
		assert.Equal(t, "user-smr", *result)
	})

	t.Run("arg overrides project config", func(t *testing.T) {
		projCfg := config.NewConfig(nil)
		_ = projCfg.Set(
			"pipeline.config.applicationServiceManagementReference",
			"proj-smr")

		result := resolveSmr("arg-wins", projCfg, config.NewEmptyConfig())
		require.NotNil(t, result)
		assert.Equal(t, "arg-wins", *result)
	})

	t.Run("project config overrides user config", func(t *testing.T) {
		projCfg := config.NewConfig(nil)
		_ = projCfg.Set(
			"pipeline.config.applicationServiceManagementReference",
			"proj-smr")
		userCfg := config.NewConfig(nil)
		_ = userCfg.Set(
			"pipeline.config.applicationServiceManagementReference",
			"user-smr")

		result := resolveSmr("", projCfg, userCfg)
		require.NotNil(t, result)
		assert.Equal(t, "proj-smr", *result)
	})

	t.Run("returns nil when nothing set", func(t *testing.T) {
		result := resolveSmr(
			"",
			config.NewEmptyConfig(),
			config.NewEmptyConfig())
		assert.Nil(t, result)
	})
}

// ------------------------------------------------------------------
// gitHubActionsEnablingChoice.String()
// ------------------------------------------------------------------

func Test_gitHubActionsEnablingChoice_String(t *testing.T) {
	assert.Contains(t, manualChoice.String(), "manually enabled")
	assert.Contains(t, cancelChoice.String(), "Exit without pushing")
}

func Test_gitHubActionsEnablingChoice_String_panic(t *testing.T) {
	assert.Panics(t, func() {
		_ = gitHubActionsEnablingChoice(99).String()
	})
}

// ------------------------------------------------------------------
// workflow (GitHub CiPipeline) name() and url()
// ------------------------------------------------------------------

func Test_workflow_CiPipeline(t *testing.T) {
	w := &workflow{
		repoDetails: &gitRepositoryDetails{
			url: "https://github.com/Azure/azure-dev",
		},
	}

	assert.Equal(t, "actions", w.name())
	assert.Equal(t,
		"https://github.com/Azure/azure-dev/actions",
		w.url())
}

// ------------------------------------------------------------------
// pipeline (AzDo CiPipeline) name() and url()
// ------------------------------------------------------------------

func Test_azdoPipeline_CiPipeline(t *testing.T) {
	defName := "my-pipeline"
	defId := 42

	p := &pipeline{
		repoDetails: &AzdoRepositoryDetails{
			repoWebUrl: "https://dev.azure.com/org/project/_git/repo",
			buildDefinition: &build.BuildDefinition{
				Name: &defName,
				Id:   &defId,
			},
		},
	}

	assert.Equal(t, "my-pipeline", p.name())
	assert.Equal(t,
		"https://dev.azure.com/org/project/_build?definitionId=42",
		p.url())
}

// ------------------------------------------------------------------
// mergeProjectVariablesAndSecrets — providerParameters
// ------------------------------------------------------------------

func Test_mergeProjectVariablesAndSecrets_providerParams(
	t *testing.T,
) {
	t.Run("single env var secret via provider param", func(t *testing.T) {
		params := []provisioning.Parameter{
			{
				Name:               "dbPass",
				Value:              "s3cret",
				Secret:             true,
				LocalPrompt:        true,
				EnvVarMapping:      []string{"DB_PASSWORD"},
				UsingEnvVarMapping: false,
			},
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "s3cret", secrets["DB_PASSWORD"])
		assert.Empty(t, vars)
	})

	t.Run("single env var variable via provider param", func(t *testing.T) {
		params := []provisioning.Parameter{
			{
				Name:               "region",
				Value:              "eastus",
				Secret:             false,
				LocalPrompt:        true,
				EnvVarMapping:      []string{"AZURE_LOCATION"},
				UsingEnvVarMapping: false,
			},
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "eastus", vars["AZURE_LOCATION"])
		assert.Empty(t, secrets)
	})

	t.Run("error when local prompt and no env var mapping",
		func(t *testing.T) {
			params := []provisioning.Parameter{
				{
					Name:          "bad",
					Value:         "val",
					LocalPrompt:   true,
					EnvVarMapping: []string{},
				},
			}
			_, _, err := mergeProjectVariablesAndSecrets(
				nil, nil, map[string]string{}, map[string]string{},
				params, map[string]string{})
			require.Error(t, err)
			assert.Contains(t, err.Error(),
				"local prompt and it has not a mapped environment variable")
		})

	t.Run(
		"error when local prompt and multiple env var mappings",
		func(t *testing.T) {
			params := []provisioning.Parameter{
				{
					Name:          "multi",
					Value:         "val",
					LocalPrompt:   true,
					EnvVarMapping: []string{"A", "B"},
				},
			}
			_, _, err := mergeProjectVariablesAndSecrets(
				nil, nil, map[string]string{}, map[string]string{},
				params, map[string]string{})
			require.Error(t, err)
			assert.Contains(t, err.Error(),
				"more than one mapped environment variable")
		})

	t.Run("multi env var non-prompt uses env values", func(t *testing.T) {
		params := []provisioning.Parameter{
			{
				Name:          "multiEnv",
				Secret:        false,
				LocalPrompt:   false,
				EnvVarMapping: []string{"VAR_A", "VAR_B"},
			},
		}
		env := map[string]string{
			"VAR_A": "valA",
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, env)
		require.NoError(t, err)
		assert.Equal(t, "valA", vars["VAR_A"])
		// VAR_B not in env, so not set
		_, hasB := vars["VAR_B"]
		assert.False(t, hasB)
		assert.Empty(t, secrets)
	})

	t.Run("multi env var secret uses env values", func(t *testing.T) {
		params := []provisioning.Parameter{
			{
				Name:          "multiSecret",
				Secret:        true,
				LocalPrompt:   false,
				EnvVarMapping: []string{"SEC_A", "SEC_B"},
			},
		}
		env := map[string]string{
			"SEC_A": "secretA",
			"SEC_B": "secretB",
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, env)
		require.NoError(t, err)
		assert.Equal(t, "secretA", secrets["SEC_A"])
		assert.Equal(t, "secretB", secrets["SEC_B"])
		assert.Empty(t, vars)
	})

	t.Run("no env var mapping and no local prompt is skipped",
		func(t *testing.T) {
			params := []provisioning.Parameter{
				{
					Name:          "skipped",
					Value:         "val",
					LocalPrompt:   false,
					EnvVarMapping: []string{},
				},
			}
			vars, secrets, err := mergeProjectVariablesAndSecrets(
				nil, nil, map[string]string{}, map[string]string{},
				params, map[string]string{})
			require.NoError(t, err)
			assert.Empty(t, vars)
			assert.Empty(t, secrets)
		})

	t.Run(
		"single env var non-prompt non-UsingEnvVarMapping skipped",
		func(t *testing.T) {
			params := []provisioning.Parameter{
				{
					Name:               "notUsed",
					Value:              "val",
					LocalPrompt:        false,
					UsingEnvVarMapping: false,
					EnvVarMapping:      []string{"SOME_VAR"},
				},
			}
			vars, secrets, err := mergeProjectVariablesAndSecrets(
				nil, nil, map[string]string{}, map[string]string{},
				params, map[string]string{})
			require.NoError(t, err)
			assert.Empty(t, vars)
			assert.Empty(t, secrets)
		})

	t.Run(
		"single env var non-prompt UsingEnvVarMapping is set",
		func(t *testing.T) {
			params := []provisioning.Parameter{
				{
					Name:               "used",
					Value:              "myVal",
					Secret:             false,
					LocalPrompt:        false,
					UsingEnvVarMapping: true,
					EnvVarMapping:      []string{"MY_VAR"},
				},
			}
			vars, secrets, err := mergeProjectVariablesAndSecrets(
				nil, nil, map[string]string{}, map[string]string{},
				params, map[string]string{})
			require.NoError(t, err)
			assert.Equal(t, "myVal", vars["MY_VAR"])
			assert.Empty(t, secrets)
		})

	t.Run("project vars override provider params", func(t *testing.T) {
		params := []provisioning.Parameter{
			{
				Name:               "region",
				Value:              "westus",
				LocalPrompt:        true,
				EnvVarMapping:      []string{"AZURE_LOCATION"},
				UsingEnvVarMapping: false,
			},
		}
		env := map[string]string{
			"AZURE_LOCATION": "eastus2",
		}
		vars, _, err := mergeProjectVariablesAndSecrets(
			[]string{"AZURE_LOCATION"}, nil,
			map[string]string{}, map[string]string{},
			params, env)
		require.NoError(t, err)
		// project var from env overrides provider param
		assert.Equal(t, "eastus2", vars["AZURE_LOCATION"])
	})
}

// ------------------------------------------------------------------
// mergeProjectVariablesAndSecrets — explicit empty values
// ------------------------------------------------------------------

func Test_mergeProjectVariablesAndSecrets_explicitEmptyEnvPreserved(
	t *testing.T,
) {
	env := map[string]string{
		"VAR1": "", "VAR2": "value2", "SECRET": "",
	}
	vars, secrets, err := mergeProjectVariablesAndSecrets(
		[]string{"VAR1", "VAR2"}, []string{"SECRET"},
		map[string]string{"VAR1": "old-value"}, map[string]string{"SECRET": "old-secret"},
		nil, env)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"VAR1": "", "VAR2": "value2"}, vars)
	require.Equal(t, map[string]string{"SECRET": ""}, secrets)
}

func Test_mergeProjectVariablesAndSecrets_sourcePresence(t *testing.T) {
	const source = "AZD_TEST_PIPELINE_SOURCE_PRESENCE"
	tests := []struct {
		name           string
		project        map[string]string
		process        string
		processPresent bool
		want           string
		wantPresent    bool
	}{
		{name: "project wins", project: map[string]string{source: "project"}, process: "ambient",
			processPresent: true, want: "project", wantPresent: true},
		{name: "empty project wins", project: map[string]string{source: ""}, process: "ambient",
			processPresent: true, wantPresent: true},
		{name: "missing project uses process", process: "ambient", processPresent: true,
			want: "ambient", wantPresent: true},
		{name: "empty process stays present", processPresent: true, wantPresent: true},
		{name: "missing source stays missing"},
	}
	for _, tt := range tests {
		for _, secret := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/secret=%t", tt.name, secret), func(t *testing.T) {
				t.Setenv(source, tt.process)
				if !tt.processPresent {
					require.NoError(t, os.Unsetenv(source))
				}
				variables, secrets, err := mergeProjectVariablesAndSecrets(
					nil, nil, map[string]string{}, map[string]string{},
					[]provisioning.Parameter{{Name: "env.INPUT", Secret: secret, EnvVarMapping: []string{source}}},
					tt.project,
				)
				require.NoError(t, err)
				values := variables
				if secret {
					values = secrets
				}
				value, present := values[source]
				require.Equal(t, tt.wantPresent, present)
				require.Equal(t, tt.want, value)
			})
		}
	}
}

func Test_mergeProjectVariablesAndSecrets_derivedSingleSourceExportsRawValue(t *testing.T) {
	for _, tt := range []struct {
		name    string
		raw     string
		derived any
	}{
		{name: "composite", raw: "host", derived: "https://host/api"},
		{name: "number", raw: "003", derived: int64(3)},
		{name: "boolean", raw: "TRUE", derived: true},
		{name: "default", raw: "", derived: "fallback"},
	} {
		for _, secret := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/secret=%t", tt.name, secret), func(t *testing.T) {
				t.Parallel()
				variables, secrets, err := mergeProjectVariablesAndSecrets(
					nil, nil, map[string]string{}, map[string]string{},
					[]provisioning.Parameter{{
						Name: "derived", Value: tt.derived, Secret: secret, EnvVarMapping: []string{"SOURCE"},
					}}, map[string]string{"SOURCE": tt.raw},
				)
				require.NoError(t, err)
				values := variables
				if secret {
					values = secrets
				}
				require.Equal(t, map[string]string{"SOURCE": tt.raw}, values)
			})
		}
	}
}

func Test_mergeProjectVariablesAndSecrets_emptyExpressionSourcePreservesDefault(t *testing.T) {
	const source = "AZD_TEST_PIPELINE_EMPTY_PROJECT_SOURCE"
	t.Setenv(source, "ambient-value")
	mockContext := mocks.NewMockContext(t.Context())
	provider := &paramsOnlyProvider{parameters: []provisioning.Parameter{{
		Name: "input", Value: "fallback", EnvVarMapping: []string{"LOCAL_INPUT"}, UsingEnvVarMapping: true,
	}}}
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, nil, nil,
		environment.NewWithValues("test", map[string]string{source: ""}),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	expression := osutil.NewExpandableString("${" + source + ":-fallback}")
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep, Env: osutil.ExpandableMap{"LOCAL_INPUT": expression},
	}))
	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	variables, _, err := mergeProjectVariablesAndSecrets(
		nil, nil, map[string]string{}, map[string]string{}, parameters, map[string]string{source: ""},
	)
	require.NoError(t, err)
	value, present := variables[source]
	require.True(t, present)
	require.Empty(t, value)
	ciValue, err := expression.Envsubst(func(name string) string { return variables[name] })
	require.NoError(t, err)
	require.Equal(t, "fallback", ciValue)
}

// ------------------------------------------------------------------
// mergeProjectVariablesAndSecrets — initial values cloned
// ------------------------------------------------------------------

func Test_mergeProjectVariablesAndSecrets_initialNotMutated(
	t *testing.T,
) {
	initialVars := map[string]string{"INIT": "orig"}
	initialSecrets := map[string]string{"SEC": "orig"}
	env := map[string]string{"EXTRA": "val"}

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		[]string{"EXTRA"}, nil,
		initialVars, initialSecrets,
		nil, env)
	require.NoError(t, err)

	// returned maps have both initial and extra
	assert.Equal(t, "orig", vars["INIT"])
	assert.Equal(t, "val", vars["EXTRA"])
	assert.Equal(t, "orig", secrets["SEC"])

	// original maps not mutated
	_, hasExtra := initialVars["EXTRA"]
	assert.False(t, hasExtra,
		"initialVars should not be mutated")
}

// ------------------------------------------------------------------
// escapeValuesForPipeline — additional cases
// ------------------------------------------------------------------

func Test_escapeValuesForPipeline_nilMap(t *testing.T) {
	// should not panic on nil map
	assert.NotPanics(t, func() {
		escapeValuesForPipeline(nil)
	})
}

func Test_escapeValuesForPipeline_noModification(t *testing.T) {
	values := map[string]string{
		"plain": "hello world",
	}
	escapeValuesForPipeline(values)
	assert.Equal(t, "hello world", values["plain"])
}

// ------------------------------------------------------------------
// pipelineProviderFiles var validation
// ------------------------------------------------------------------

func Test_pipelineProviderFiles_knownProviders(t *testing.T) {
	ghInfo, ok := pipelineProviderFiles[ciProviderGitHubActions]
	require.True(t, ok, "GitHub Actions entry missing")
	assert.NotEmpty(t, ghInfo.RootDirectories)
	assert.NotEmpty(t, ghInfo.PipelineDirectories)
	assert.NotEmpty(t, ghInfo.Files)
	assert.NotEmpty(t, ghInfo.DefaultFile)

	azdoInfo, ok := pipelineProviderFiles[ciProviderAzureDevOps]
	require.True(t, ok, "Azure DevOps entry missing")
	assert.NotEmpty(t, azdoInfo.RootDirectories)
	assert.NotEmpty(t, azdoInfo.PipelineDirectories)
	assert.NotEmpty(t, azdoInfo.Files)
	assert.NotEmpty(t, azdoInfo.DefaultFile)
}

// ------------------------------------------------------------------
// constants sanity
// ------------------------------------------------------------------

func Test_constants(t *testing.T) {
	assert.Equal(t, ciProviderType("github"), ciProviderGitHubActions)
	assert.Equal(t, ciProviderType("azdo"), ciProviderAzureDevOps)
	assert.Equal(t, infraProviderType("bicep"), infraProviderBicep)
	assert.Equal(t, infraProviderType("terraform"), infraProviderTerraform)
	assert.Equal(t, infraProviderType(""), infraProviderUndefined)
	assert.Equal(t, PipelineAuthType("federated"), AuthTypeFederated)
	assert.Equal(t,
		PipelineAuthType("client-credentials"),
		AuthTypeClientCredentials)
	assert.Equal(t, "AZD_PIPELINE_PROVIDER", envPersistedKey)
}

// ------------------------------------------------------------------
// servicePrincipal lookup strategy
// ------------------------------------------------------------------

type mockEntraIdService struct {
	entraid.EntraIdService
	getSpResult *graphsdk.ServicePrincipal
	getSpErr    error
}

func (m *mockEntraIdService) GetServicePrincipal(
	_ context.Context, _, _ string,
) (*graphsdk.ServicePrincipal, error) {
	return m.getSpResult, m.getSpErr
}

func Test_servicePrincipal(t *testing.T) {
	ctx := t.Context()

	t.Run("uses principal-id arg when set", func(t *testing.T) {
		orgId := "org-id-123"
		sp := &graphsdk.ServicePrincipal{
			AppId:                  "app-id",
			DisplayName:            "my-sp",
			AppOwnerOrganizationId: &orgId,
		}
		svc := &mockEntraIdService{getSpResult: sp}

		result, err := servicePrincipal(ctx, "", "sub-1",
			&PipelineManagerArgs{
				PipelineServicePrincipalId: "app-id",
			}, svc)
		require.NoError(t, err)
		assert.Equal(t, "app-id", result.appIdOrName)
		assert.Equal(t, "my-sp", result.applicationName)
		assert.Equal(t, lookupKindPrincipalId, result.lookupKind)
		assert.NotNil(t, result.servicePrincipal)
	})

	t.Run("uses principal-name arg when set", func(t *testing.T) {
		sp := &graphsdk.ServicePrincipal{
			AppId:       "app-from-name",
			DisplayName: "sp-name",
		}
		svc := &mockEntraIdService{getSpResult: sp}

		result, err := servicePrincipal(ctx, "", "sub-1",
			&PipelineManagerArgs{
				PipelineServicePrincipalName: "sp-name",
			}, svc)
		require.NoError(t, err)
		assert.Equal(t, "app-from-name", result.appIdOrName)
		assert.Equal(t, lookupKindPrincipleName, result.lookupKind)
	})

	t.Run("uses env var when no args", func(t *testing.T) {
		orgId := "org-id"
		sp := &graphsdk.ServicePrincipal{
			AppId:                  "env-client-id",
			DisplayName:            "env-sp",
			AppOwnerOrganizationId: &orgId,
		}
		svc := &mockEntraIdService{getSpResult: sp}

		result, err := servicePrincipal(
			ctx, "env-client-id", "sub-1",
			&PipelineManagerArgs{}, svc)
		require.NoError(t, err)
		assert.Equal(t, "env-client-id", result.appIdOrName)
		assert.Equal(t,
			lookupKindEnvironmentVariable, result.lookupKind)
	})

	t.Run("creates new when no args and no env", func(t *testing.T) {
		svc := &mockEntraIdService{}

		result, err := servicePrincipal(
			ctx, "", "sub-1",
			&PipelineManagerArgs{}, svc)
		require.NoError(t, err)
		assert.Contains(t, result.applicationName, "az-dev-")
		assert.Nil(t, result.servicePrincipal)
	})

	t.Run(
		"error when principal-id not found",
		func(t *testing.T) {
			svc := &mockEntraIdService{
				getSpErr: assert.AnError,
			}
			_, err := servicePrincipal(ctx, "", "sub-1",
				&PipelineManagerArgs{
					PipelineServicePrincipalId: "missing-id",
				}, svc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing-id")
			assert.Contains(t, err.Error(), "--principal-id")
		})

	t.Run("error when env var not found", func(t *testing.T) {
		svc := &mockEntraIdService{
			getSpErr: assert.AnError,
		}
		_, err := servicePrincipal(
			ctx, "env-client", "sub-1",
			&PipelineManagerArgs{}, svc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env-client")
		assert.Contains(t, err.Error(),
			AzurePipelineClientIdEnvVarName)
	})

	t.Run(
		"name not found returns name for creation",
		func(t *testing.T) {
			svc := &mockEntraIdService{
				getSpErr: assert.AnError,
			}
			result, err := servicePrincipal(ctx, "", "sub-1",
				&PipelineManagerArgs{
					PipelineServicePrincipalName: "new-sp",
				}, svc)
			require.NoError(t, err)
			assert.Equal(t, "new-sp", result.appIdOrName)
			assert.Equal(t, "new-sp", result.applicationName)
		})
}

// ------------------------------------------------------------------
// GitHub credentialOptions
// ------------------------------------------------------------------

func Test_GitHubCiProvider_credentialOptions(t *testing.T) {
	// Helper to create a provider with mocked OIDC API returning default
	newProvider := func(t *testing.T) *GitHubCiProvider {
		t.Helper()
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)

		// Mock OIDC API to return default config for any repo
		mockContext.CommandRunner.When(
			func(args exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "oidc/customization/sub")
			},
		).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "HTTP 404: Not Found"),
				fmt.Errorf("HTTP 404: Not Found")
		})

		return &GitHubCiProvider{
			ghCli: github.NewGitHubCli(
				mockContext.Console,
				mockContext.CommandRunner,
			),
			console: mockContext.Console,
		}
	}

	t.Run("client-credentials auth", func(t *testing.T) {
		provider := newProvider(t)
		opts, err := provider.credentialOptions(t.Context(),
			&gitRepositoryDetails{
				owner:    "Azure",
				repoName: "azure-dev",
				branch:   "main",
			},
			provisioning.Options{},
			AuthTypeClientCredentials,
			&entraid.AzureCredentials{})
		require.NoError(t, err)
		assert.True(t, opts.EnableClientCredentials)
		assert.False(t, opts.EnableFederatedCredentials)
	})

	t.Run("federated auth creates credentials", func(t *testing.T) {
		provider := newProvider(t)
		opts, err := provider.credentialOptions(t.Context(),
			&gitRepositoryDetails{
				owner:    "Azure",
				repoName: "azure-dev",
				branch:   "feature",
			},
			provisioning.Options{},
			AuthTypeFederated,
			&entraid.AzureCredentials{})
		require.NoError(t, err)
		assert.False(t, opts.EnableClientCredentials)
		assert.True(t, opts.EnableFederatedCredentials)
		// Should have pull_request + feature + main creds
		require.Len(t, opts.FederatedCredentialOptions, 3)
		// First is always pull_request
		assert.Contains(t,
			opts.FederatedCredentialOptions[0].Subject,
			"pull_request")
	})

	t.Run(
		"federated on main branch - no duplicate",
		func(t *testing.T) {
			provider := newProvider(t)
			opts, err := provider.credentialOptions(t.Context(),
				&gitRepositoryDetails{
					owner:    "Azure",
					repoName: "azure-dev",
					branch:   "main",
				},
				provisioning.Options{},
				AuthTypeFederated,
				&entraid.AzureCredentials{})
			require.NoError(t, err)
			// pull_request + main (no duplicate)
			require.Len(t, opts.FederatedCredentialOptions, 2)
		})

	t.Run("empty auth type defaults to federated",
		func(t *testing.T) {
			provider := newProvider(t)
			opts, err := provider.credentialOptions(t.Context(),
				&gitRepositoryDetails{
					owner:    "Azure",
					repoName: "azure-dev",
					branch:   "dev",
				},
				provisioning.Options{},
				"",
				&entraid.AzureCredentials{})
			require.NoError(t, err)
			assert.True(t, opts.EnableFederatedCredentials)
			assert.False(t, opts.EnableClientCredentials)
		})

	t.Run(
		"unknown auth type returns empty options",
		func(t *testing.T) {
			provider := newProvider(t)
			opts, err := provider.credentialOptions(t.Context(),
				&gitRepositoryDetails{
					owner:    "Azure",
					repoName: "azure-dev",
					branch:   "main",
				},
				provisioning.Options{},
				PipelineAuthType("unknown-type"),
				&entraid.AzureCredentials{})
			require.NoError(t, err)
			assert.False(t, opts.EnableClientCredentials)
			assert.False(t, opts.EnableFederatedCredentials)
		})

	t.Run(
		"federated credential names sanitized",
		func(t *testing.T) {
			provider := newProvider(t)
			opts, err := provider.credentialOptions(t.Context(),
				&gitRepositoryDetails{
					owner:    "my.org",
					repoName: "my.repo",
					branch:   "feat/branch",
				},
				provisioning.Options{},
				AuthTypeFederated,
				&entraid.AzureCredentials{})
			require.NoError(t, err)
			for _, cred := range opts.FederatedCredentialOptions {
				// No dots or slashes in name
				assert.NotContains(t, cred.Name, ".")
				assert.NotContains(t, cred.Name, "/")
			}
		})
}

// ------------------------------------------------------------------
// PipelineManager.SetParameters
// ------------------------------------------------------------------

func Test_PipelineManager_SetParameters(t *testing.T) {
	t.Run("initializes configOptions if nil", func(t *testing.T) {
		pm := &PipelineManager{}
		pm.SetParameters([]provisioning.Parameter{
			{Name: "param1"},
		})
		require.NotNil(t, pm.configOptions)
		require.Len(t, pm.configOptions.providerParameters, 1)
		assert.Equal(t, "param1",
			pm.configOptions.providerParameters[0].Name)
	})

	t.Run("replaces existing parameters", func(t *testing.T) {
		pm := &PipelineManager{
			configOptions: &configurePipelineOptions{
				providerParameters: []provisioning.Parameter{
					{Name: "old"},
				},
			},
		}
		pm.SetParameters([]provisioning.Parameter{
			{Name: "new1"}, {Name: "new2"},
		})
		require.Len(t, pm.configOptions.providerParameters, 2)
	})

	t.Run("nil parameters clears list", func(t *testing.T) {
		pm := &PipelineManager{
			configOptions: &configurePipelineOptions{
				providerParameters: []provisioning.Parameter{
					{Name: "old"},
				},
			},
		}
		pm.SetParameters(nil)
		assert.Nil(t, pm.configOptions.providerParameters)
	})
}

// ------------------------------------------------------------------
// projectProperties / configurePipelineOptions struct sanity
// ------------------------------------------------------------------

func Test_projectProperties_fields(t *testing.T) {
	props := projectProperties{
		CiProvider:    ciProviderGitHubActions,
		InfraProvider: infraProviderBicep,
		RepoRoot:      "/tmp/repo",
		HasAppHost:    true,
		BranchName:    "main",
		AuthType:      AuthTypeFederated,
		Variables:     []string{"VAR1"},
		Secrets:       []string{"SEC1"},
	}
	assert.Equal(t, ciProviderGitHubActions, props.CiProvider)
	assert.Equal(t, infraProviderBicep, props.InfraProvider)
	assert.True(t, props.HasAppHost)
	assert.Equal(t, "main", props.BranchName)
	assert.Equal(t, AuthTypeFederated, props.AuthType)
}

// ------------------------------------------------------------------
// GitHub regex patterns
// ------------------------------------------------------------------

func Test_gitHubRemoteRegexPatterns(t *testing.T) {
	t.Run("https url with .git", func(t *testing.T) {
		m := gitHubRemoteHttpsUrlRegex.FindStringSubmatch(
			"https://github.com/Azure/azure-dev.git")
		require.NotNil(t, m)
		assert.Equal(t, "Azure/azure-dev", m[1])
	})

	t.Run("https url without .git", func(t *testing.T) {
		m := gitHubRemoteHttpsUrlRegex.FindStringSubmatch(
			"https://github.com/Azure/azure-dev")
		require.NotNil(t, m)
		assert.Equal(t, "Azure/azure-dev", m[1])
	})

	t.Run("ssh url with .git", func(t *testing.T) {
		m := gitHubRemoteGitUrlRegex.FindStringSubmatch(
			"git@github.com:Azure/azure-dev.git")
		require.NotNil(t, m)
		assert.Equal(t, "Azure/azure-dev", m[1])
	})

	t.Run("ssh url without .git", func(t *testing.T) {
		m := gitHubRemoteGitUrlRegex.FindStringSubmatch(
			"git@github.com:Azure/azure-dev")
		require.NotNil(t, m)
		assert.Equal(t, "Azure/azure-dev", m[1])
	})

	t.Run("https www prefix", func(t *testing.T) {
		m := gitHubRemoteHttpsUrlRegex.FindStringSubmatch(
			"https://www.github.com/owner/repo.git")
		require.NotNil(t, m)
		assert.Equal(t, "owner/repo", m[1])
	})

	t.Run("ssh regex does not match https", func(t *testing.T) {
		m := gitHubRemoteGitUrlRegex.FindStringSubmatch(
			"https://github.com/Azure/azure-dev.git")
		assert.Nil(t, m)
	})
}

// ------------------------------------------------------------------
// DefaultRoleNames
// ------------------------------------------------------------------

func Test_DefaultRoleNames(t *testing.T) {
	require.Len(t, DefaultRoleNames, 2)
	assert.Contains(t, DefaultRoleNames, "Contributor")
	assert.Contains(t, DefaultRoleNames, "User Access Administrator")
}

// ------------------------------------------------------------------
// mergeProjectVariablesAndSecrets — layer inputs
// ------------------------------------------------------------------

type paramsOnlyProvider struct {
	provisioning.Provider
	parameters []provisioning.Parameter
}

func (p *paramsOnlyProvider) Initialize(context.Context, string, provisioning.Options) error {
	return nil
}

func (p *paramsOnlyProvider) Parameters(context.Context) ([]provisioning.Parameter, error) {
	return p.parameters, nil
}

// CI exports source variables, not the provider's already-expanded input values.
func Test_mergeProjectVariablesAndSecrets_usesStorageProjectVariables(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := &paramsOnlyProvider{parameters: []provisioning.Parameter{
		{Name: "instanceName", Value: "primary",
			EnvVarMapping: []string{"STORAGE_INSTANCE_NAME"}, UsingEnvVarMapping: true},
		{Name: "location", Value: "eastus", EnvVarMapping: []string{"AZURE_LOCATION"}, UsingEnvVarMapping: true},
		{Name: "environmentName", Value: "the-environment-name",
			EnvVarMapping: []string{"AZURE_ENV_NAME"}, UsingEnvVarMapping: true},
	}}
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) { return provisioning.Bicep, nil },
		nil,
		environment.New("test"),
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)

	err := manager.Initialize(t.Context(), "", provisioning.Options{
		Name:     "primary-storage",
		Provider: provisioning.Bicep,
		Path:     "infra/storage",
		Env: osutil.ExpandableMap{
			"STORAGE_INSTANCE_NAME": osutil.NewExpandableString("${PRIMARY_STORAGE_INSTANCE_NAME}"),
		},
	})
	require.NoError(t, err)

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{
		{Name: "instanceName",
			EnvVarMapping: []string{"PRIMARY_STORAGE_INSTANCE_NAME"}, UsingEnvVarMapping: true},
		{Name: "location", Value: "eastus", EnvVarMapping: []string{"AZURE_LOCATION"}, UsingEnvVarMapping: true},
		{Name: "environmentName", Value: "the-environment-name",
			EnvVarMapping: []string{"AZURE_ENV_NAME"}, UsingEnvVarMapping: true},
	}, parameters)
	require.Equal(t, []string{"STORAGE_INSTANCE_NAME"}, provider.parameters[0].EnvVarMapping)

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		nil, nil, map[string]string{}, map[string]string{}, parameters,
		map[string]string{
			"PRIMARY_STORAGE_INSTANCE_NAME": "primary",
			"STORAGE_INSTANCE_NAME":         "must-not-be-exported",
			"AZURE_LOCATION":                "eastus",
			"AZURE_ENV_NAME":                "the-environment-name",
		})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"PRIMARY_STORAGE_INSTANCE_NAME": "primary",
		"AZURE_LOCATION":                "eastus",
		"AZURE_ENV_NAME":                "the-environment-name",
	}, vars)
	require.Empty(t, secrets)
}

func Test_mergeProjectVariablesAndSecrets_explicitInputsSecretsAndComposites(t *testing.T) {
	provider := &paramsOnlyProvider{parameters: []provisioning.Parameter{
		{Name: "location", Value: "eastus",
			EnvVarMapping: []string{"PROVIDER_LOCATION"}, UsingEnvVarMapping: true},
		{Name: "dbPass", Value: "password=s3cret;host=db.example", Secret: true,
			EnvVarMapping: []string{"PROVIDER_DB_CONN"}, UsingEnvVarMapping: true},
		{Name: "plain", Value: "kept", EnvVarMapping: []string{"UNALIASED"}, UsingEnvVarMapping: true},
		{Name: "literal", Value: "fixed", EnvVarMapping: []string{"LITERAL"}},
		// CI reads each source variable separately for a combined parameter
		// value (ex: "${PROVIDER_LOCATION}${UNALIASED_MULTI}").
		// It must read the project variable PROJECT_LOCATION, not the provider variable PROVIDER_LOCATION.
		{Name: "multi", Value: "x", EnvVarMapping: []string{"PROVIDER_LOCATION", "UNALIASED_MULTI"}},
	}}

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) { return provisioning.Bicep, nil },
		nil,
		environment.New("test"),
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)

	err := manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Env: osutil.ExpandableMap{
			"PROVIDER_LOCATION": osutil.NewExpandableString("${PROJECT_LOCATION}"),
			"PROVIDER_DB_CONN": osutil.NewExpandableString(
				"password=${PROJECT_DB_CONN};host=${PROJECT_DB_HOST:-${DEFAULT_DB_HOST}}"),
			"LITERAL": osutil.NewExpandableString("fixed"),
		},
	})
	require.NoError(t, err)

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{
		{Name: "location", EnvVarMapping: []string{"PROJECT_LOCATION"}, UsingEnvVarMapping: true},
		{Name: "dbPass", Secret: true,
			EnvVarMapping:      []string{"DEFAULT_DB_HOST", "PROJECT_DB_CONN", "PROJECT_DB_HOST"},
			UsingEnvVarMapping: true},
		{Name: "plain", Value: "kept", EnvVarMapping: []string{"UNALIASED"}, UsingEnvVarMapping: true},
		{Name: "literal"},
		{Name: "multi", EnvVarMapping: []string{"PROJECT_LOCATION", "UNALIASED_MULTI"}},
	}, parameters)
	require.Equal(t, "password=s3cret;host=db.example", provider.parameters[1].Value)
	require.False(t, provider.parameters[1].LocalPrompt)
	require.Equal(t, []string{"PROVIDER_DB_CONN"}, provider.parameters[1].EnvVarMapping)

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		nil, nil, map[string]string{}, map[string]string{}, parameters,
		map[string]string{
			"PROJECT_LOCATION":  "project-location",
			"PROVIDER_LOCATION": "provider-location",
			"UNALIASED_MULTI":   "unaliased-multi",
			"PROJECT_DB_CONN":   "s3cret",
			"PROJECT_DB_HOST":   "db.example",
			"DEFAULT_DB_HOST":   "fallback.example",
			"PROVIDER_DB_CONN":  "must-not-be-exported",
			"LITERAL":           "must-not-be-exported",
		})
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"PROJECT_LOCATION": "project-location",
		"UNALIASED":        "kept",
		"UNALIASED_MULTI":  "unaliased-multi",
	}, vars)
	require.Equal(t, map[string]string{
		"PROJECT_DB_CONN": "s3cret", "PROJECT_DB_HOST": "db.example", "DEFAULT_DB_HOST": "fallback.example",
	}, secrets)
}

func Test_mergeProjectVariablesAndSecrets_unreportedInputSourcesAreSecrets(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := &paramsOnlyProvider{}
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Terraform), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) { return provisioning.Terraform, nil },
		nil, environment.New("test"), mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Terraform,
		Env: osutil.ExpandableMap{
			"LOCAL_ENDPOINT": osutil.NewExpandableString("${BASE_URL}/${LAYER_OUTPUT}/${PREFIX:-fallback}"),
			"LOCAL_INTERNAL": osutil.NewExpandableString("${LAYER_OUTPUT}"),
			"LOCAL_LITERAL":  osutil.NewExpandableString("fixed"),
		},
		VirtualEnv: map[string]string{"LAYER_OUTPUT": ""},
	}))
	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{{
		Name: "env.LOCAL_ENDPOINT", Secret: true,
		EnvVarMapping: []string{"BASE_URL", "PREFIX"}, UsingEnvVarMapping: true,
	}}, parameters)

	variables, secrets, err := mergeProjectVariablesAndSecrets(
		nil, nil, map[string]string{}, map[string]string{}, parameters,
		map[string]string{
			"BASE_URL": "https://example.test", "PREFIX": "api", "LAYER_OUTPUT": "created-by-earlier-layer",
			"LOCAL_ENDPOINT": "must-not-be-exported", "LOCAL_LITERAL": "must-not-be-exported",
		})
	require.NoError(t, err)
	require.Empty(t, variables)
	require.Equal(t, map[string]string{"BASE_URL": "https://example.test", "PREFIX": "api"}, secrets)
}

func Test_mergeProjectVariablesAndSecrets_sharedInputSourceSecretWins(t *testing.T) {
	for _, secretFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("secret-first=%t", secretFirst), func(t *testing.T) {
			parameters := []provisioning.Parameter{
				{Name: "public", EnvVarMapping: []string{"SHARED_INPUT"}},
				{Name: "secret", Secret: true, EnvVarMapping: []string{"SHARED_INPUT"}},
			}
			if secretFirst {
				parameters[0], parameters[1] = parameters[1], parameters[0]
			}
			variables, secrets, err := mergeProjectVariablesAndSecrets(
				[]string{"SHARED_INPUT"}, nil, map[string]string{"SHARED_INPUT": "initial-value"},
				map[string]string{}, parameters, map[string]string{"SHARED_INPUT": "source-value"},
			)
			require.NoError(t, err)
			require.Empty(t, variables)
			require.Equal(t, map[string]string{"SHARED_INPUT": "source-value"}, secrets)
		})
	}
}

func Test_generatePipelineDefinition_sharedInputSourcesAreUnique(t *testing.T) {
	for _, provider := range []ciProviderType{ciProviderGitHubActions, ciProviderAzureDevOps} {
		t.Run(string(provider), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "azure-dev.yml")
			require.NoError(t, generatePipelineDefinition(path, projectProperties{
				CiProvider: provider, InfraProvider: infraProviderBicep, BranchName: "main", AuthType: AuthTypeFederated,
				Variables: []string{"SHARED_INPUT", "NORMAL_INPUT", "NORMAL_INPUT"},
				Secrets:   []string{"SHARED_INPUT"},
				providerParameters: []provisioning.Parameter{
					{Name: "public", EnvVarMapping: []string{"SHARED_INPUT", "NORMAL_INPUT", "NORMAL_INPUT"}},
					{Name: "secret", Secret: true, EnvVarMapping: []string{"SHARED_INPUT", "SHARED_INPUT"}},
				},
			}))
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			type step struct {
				Name        string            `yaml:"name"`
				DisplayName string            `yaml:"displayName"`
				Env         map[string]string `yaml:"env"`
			}
			var definition struct {
				Jobs map[string]struct {
					Env   map[string]string `yaml:"env"`
					Steps []step            `yaml:"steps"`
				} `yaml:"jobs"`
				Steps []step `yaml:"steps"`
			}
			// Decoding maps rejects duplicate YAML keys within each environment block.
			require.NoError(t, yaml.Unmarshal(contents, &definition))
			steps := definition.Steps
			if provider == ciProviderGitHubActions {
				build, found := definition.Jobs["build"]
				require.True(t, found)
				require.NotContains(t, build.Env, "SHARED_INPUT")
				require.Equal(t, "${{ vars.NORMAL_INPUT }}", build.Env["NORMAL_INPUT"])
				steps = build.Steps
			}
			checked := 0
			for _, step := range steps {
				if step.Name != "Provision Infrastructure" && step.DisplayName != "Provision Infrastructure" {
					continue
				}
				checked++
				if provider == ciProviderGitHubActions {
					require.Equal(t, map[string]string{"SHARED_INPUT": "${{ secrets.SHARED_INPUT }}"}, step.Env)
				} else {
					require.Equal(t, "$(SHARED_INPUT)", step.Env["SHARED_INPUT"])
					require.Equal(t, "$(NORMAL_INPUT)", step.Env["NORMAL_INPUT"])
				}
			}
			require.Equal(t, 1, checked)
		})
	}
}
