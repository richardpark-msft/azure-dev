// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/braydonk/yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigCopyRuntimeStateMatchesLayersByName(t *testing.T) {
	t.Parallel()

	dispatcher := ext.NewEventDispatcher[ServiceLifecycleEventArgs]()
	source := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "first", Services: map[string]*ServiceConfig{"api": {EventDispatcher: dispatcher}}},
		{Name: "second", Services: map[string]*ServiceConfig{"worker": {}}},
	}}
	target := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "second", Services: map[string]*ServiceConfig{"worker": {}}},
		{Name: "first", Services: map[string]*ServiceConfig{"api": {}}},
	}}

	source.CopyRuntimeStateTo(target)

	require.Same(t, dispatcher, target.Layers[1].Services["api"].EventDispatcher)
}

func TestParseProjectLayers(t *testing.T) {
	t.Parallel()

	projectConfig, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    infra:
      - name: app-infra
        path: ./infra/app
        provider: bicep
        env:
          LOCAL_INPUT: ${SHARED_INPUT}
        outputs:
          LOCAL_OUTPUT: SHARED_OUTPUT
    services:
      api:
        project: ./src/api
        host: containerapp
        language: js
`)

	require.NoError(t, err)
	require.Len(t, projectConfig.Layers, 1)
	assert.Equal(t, "application", projectConfig.Layers[0].Name)
	require.Len(t, projectConfig.Layers[0].Infra, 1)
	assert.Equal(t, "app-infra", projectConfig.Layers[0].Infra[0].Name)
	assert.Equal(t, provisioning.Bicep, projectConfig.Layers[0].Infra[0].Provider)
	assert.Equal(t, "${SHARED_INPUT}", projectConfig.Layers[0].Infra[0].Env["LOCAL_INPUT"].Raw())
	assert.Equal(t, "SHARED_OUTPUT", projectConfig.Layers[0].Infra[0].Outputs["LOCAL_OUTPUT"])
	require.Contains(t, projectConfig.Layers[0].Services, "api")
	assert.Equal(t, "api", projectConfig.Layers[0].Services["api"].Name)
	assert.Equal(t, "application", projectConfig.Layers[0].Infra[0].Layer)
}

func layerVariableProject(format string, infra provisioning.Options) ProjectConfig {
	project := ProjectConfig{Name: "test-project"}
	if format == "infra" {
		project.Infra.Layers = []provisioning.Options{infra}
	} else {
		project.Layers = LayerConfigs{{Name: "application", Infra: []provisioning.Options{infra}}}
	}
	return project
}

func TestParseLayerEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		invalid bool
	}{
		{
			name: "same source variable",
			env:  map[string]string{"FIRST": "${SHARED}", "SECOND": "${SHARED}"},
		},
		{
			name: "literals and expressions",
			env: map[string]string{
				"LITERAL": "value with spaces", "EMPTY": "", "URL": "https://${HOST}/api",
				"DEFAULT": "${OPTIONAL:-fallback}", "MISSING": "${UNSET}",
			},
		},
		{
			name: "entries retain independent expressions",
			env:  map[string]string{"FIRST": "${SECOND}", "SECOND": "${THIRD}"},
		},
		{
			name:    "provider names differ only by case",
			env:     map[string]string{"PROVIDER": "${FIRST}", "provider": "${SECOND}"},
			invalid: true,
		},
	}

	for _, tt := range tests {
		for _, format := range []string{"infra", "project"} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				env := osutil.ExpandableMap{}
				for key, value := range tt.env {
					env[key] = osutil.NewExpandableString(value)
				}
				infra := provisioning.Options{
					Name: "producer", Provider: provisioning.Bicep, Path: "infra/producer",
					Env: env,
				}
				content, err := yaml.Marshal(layerVariableProject(format, infra))
				require.NoError(t, err)

				project, err := Parse(t.Context(), string(content))
				if tt.invalid {
					require.ErrorContains(t, err, "env input names")
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.env, project.InfrastructureConfigs()[0].Env.Raw())
				}
			})
		}
	}
}

func TestParseLayerOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outputs map[string]string
		invalid bool
	}{
		{name: "independent outputs", outputs: map[string]string{"FIRST": "PROJECT_FIRST", "SECOND": "PROJECT_SECOND"}},
		{name: "mapping applies once", outputs: map[string]string{"FIRST": "SECOND", "SECOND": "THIRD"}},
		{
			name:    "same destination",
			outputs: map[string]string{"FIRST": "SHARED", "SECOND": "SHARED"}, invalid: true,
		},
		{
			name:    "destinations differ only by case",
			outputs: map[string]string{"FIRST": "SHARED", "SECOND": "shared"}, invalid: true,
		},
		{
			name:    "provider names differ only by case",
			outputs: map[string]string{"PROVIDER": "FIRST", "provider": "SECOND"}, invalid: true,
		},
	}

	for _, tt := range tests {
		for _, format := range []string{"infra", "project"} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				infra := provisioning.Options{
					Name: "producer", Provider: provisioning.Bicep, Path: "infra/producer", Outputs: tt.outputs,
				}
				content, err := yaml.Marshal(layerVariableProject(format, infra))
				require.NoError(t, err)

				project, err := Parse(t.Context(), string(content))
				if tt.invalid {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.outputs, project.InfrastructureConfigs()[0].Outputs)
				}
			})
		}
	}
}

func TestParseLayerVariableNames(t *testing.T) {
	t.Parallel()

	names := []struct {
		name  string
		valid bool
	}{
		{name: "VALID_NAME", valid: true},
		{name: "_name0", valid: true},
		{name: "a", valid: true},
		{name: ""},
		{name: "BAD-NAME"},
		{name: "PATH=value"},
		{name: "1BAD"},
		{name: "BAD.NAME"},
		{name: "BAD NAME"},
		{name: "BAD\nNAME"},
		{name: "NAME\n"},
		{name: "caf\u00e9"},
	}
	for _, format := range []string{"infra", "project"} {
		for _, field := range []string{"env key", "output key", "output destination"} {
			for _, tt := range names {
				t.Run(fmt.Sprintf("%s/%s/%q", format, field, tt.name), func(t *testing.T) {
					t.Parallel()

					infra := provisioning.Options{
						Name: "producer", Provider: provisioning.Bicep, Path: "infra/producer",
					}
					switch field {
					case "env key":
						infra.Env = osutil.ExpandableMap{tt.name: osutil.NewExpandableString("${SHARED_VARIABLE}")}
					case "output key":
						infra.Outputs = map[string]string{tt.name: "PROJECT_VARIABLE"}
					case "output destination":
						infra.Outputs = map[string]string{"PROVIDER_VARIABLE": tt.name}
					}
					content, err := yaml.Marshal(layerVariableProject(format, infra))
					require.NoError(t, err)

					_, err = Parse(t.Context(), string(content))
					if tt.valid {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "must match ^[A-Za-z_][A-Za-z0-9_]*$")
					}
				})
			}
		}
	}
}

func TestParseLayerReservedVariables(t *testing.T) {
	t.Parallel()

	names := []string{
		environment.EnvNameEnvVarName,
		environment.LocationEnvVarName,
		environment.PrincipalIdEnvVarName,
		environment.PrincipalTypeEnvVarName,
		environment.ResourceGroupEnvVarName,
		environment.SubscriptionIdEnvVarName,
		environment.TenantIdEnvVarName,
		"AZD_INTERNAL_CONTROL",
		"azure_subscription_id",
		"azd_internal_control",
	}
	for _, format := range []string{"infra", "project"} {
		for _, field := range []string{"env key", "env source", "output key", "output destination"} {
			for _, name := range names {
				t.Run(fmt.Sprintf("%s/%s/%s", format, field, name), func(t *testing.T) {
					t.Parallel()

					infra := provisioning.Options{
						Name: "producer", Provider: provisioning.Bicep, Path: "infra/producer",
					}
					switch field {
					case "env key":
						infra.Env = osutil.ExpandableMap{name: osutil.NewExpandableString("literal")}
					case "env source":
						infra.Env = osutil.ExpandableMap{"LOCAL_INPUT": osutil.NewExpandableString("${" + name + "}")}
					case "output key":
						infra.Outputs = map[string]string{name: "PROJECT_VARIABLE"}
					case "output destination":
						infra.Outputs = map[string]string{"PROVIDER_VARIABLE": name}
					}
					content, err := yaml.Marshal(layerVariableProject(format, infra))
					require.NoError(t, err)

					_, err = Parse(t.Context(), string(content))
					if field == "env source" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "reserved")
						require.ErrorContains(t, err, name)
					}
				})
			}
		}
	}
}

func TestParseLayerVariablesRejectsRootInfra(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"env", "outputs"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(t.Context(), fmt.Sprintf(
				"name: test-project\ninfra:\n  provider: bicep\n  %s:\n    LOCAL_VARIABLE: PROJECT_VARIABLE\n", field,
			))
			require.ErrorContains(t, err, field)
		})
	}
}

func TestParseLayerCustomProviderVariables(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"infra", "project"} {
		for _, test := range []struct {
			name    string
			env     map[string]any
			invalid bool
		}{
			{name: "output only"},
			{name: "empty env", env: map[string]any{}},
			{name: "nonempty env", env: map[string]any{"LOCAL_INPUT": "${PROJECT_INPUT}"}, invalid: true},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				infra := map[string]any{
					"name": "custom", "provider": "microsoft.foundry", "path": "infra/custom",
					"outputs": map[string]any{"PROVIDER_OUTPUT": "PROJECT_OUTPUT"},
				}
				if test.env != nil {
					infra["env"] = test.env
				}
				content, err := yaml.Marshal(layerVariableDocument(format, infra))
				require.NoError(t, err)

				parsed, err := Parse(t.Context(), string(content))
				if test.invalid {
					require.ErrorContains(t, err, "env inputs are only supported")
				} else {
					require.NoError(t, err)
					require.Equal(t, "PROJECT_OUTPUT", parsed.InfrastructureConfigs()[0].Outputs["PROVIDER_OUTPUT"])
				}
			})
		}
	}
}

func TestParseProjectLayersRejectsMixedFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		topLevel string
	}{
		{name: "configured infra", topLevel: "infra:\n  provider: bicep"},
		{name: "services", topLevel: "services:\n  worker:\n    host: containerapp\n    image: example/worker:latest"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			yaml := fmt.Sprintf("name: mixed-project\n%s\n"+
				"layers:\n"+
				"  - name: application\n"+
				"    services:\n"+
				"      api:\n"+
				"        host: containerapp\n"+
				"        image: example/api:latest\n", test.topLevel)
			_, err := Parse(t.Context(), yaml)

			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
		})
	}
}

func TestParseProjectLayersRejectsResources(t *testing.T) {
	t.Parallel()

	_, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    services:
      api:
        host: containerapp
        image: example/api:latest
resources:
  storage:
    type: storage
`)

	require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'resources'")
}

func TestParseProjectLayersAllowsEmptyTopLevelInfra(t *testing.T) {
	t.Parallel()

	// This is a really small edge case, but just documenting it here to establish that it was considered
	// and it's not a big enough deal to worry about at this time - we just ignore it and use the layers they've
	// configured.
	_, err := Parse(t.Context(), `name: layered-project
# OH NO - AN EMPTY LITERAL!
infra: {}
layers:
  - name: application
    services:
      api:
        host: containerapp
        image: example/api:latest
`)

	require.NoError(t, err)
}

func TestParseProjectLayersRejectsInvalidContainers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "empty layer",
			yaml:    "name: test-project\nlayers:\n  - name: application\n",
			wantErr: "must contain infrastructure or services",
		},
		{
			name: "duplicate service",
			yaml: `name: test-project
layers:
  - name: first
    services:
      api:
        host: containerapp
        image: example/api:latest
  - name: second
    services:
      api:
        host: containerapp
        image: example/api:latest
`,
			wantErr: "service 'api' is defined in both layers",
		},
		{
			name: "duplicate infrastructure entry",
			yaml: `name: test-project
layers:
  - name: first
    infra:
      - name: shared
        provider: terraform
        path: infra/first
  - name: second
    infra:
      - name: shared
        provider: terraform
        path: infra/second
`,
			wantErr: "infrastructure entry 'shared' is defined in both layers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(t.Context(), test.yaml)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestSaveProjectLayersPreservesV2Shape(t *testing.T) {
	t.Parallel()

	projectConfig, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    infra:
      - name: app-infra
        path: ./infra/app
        provider: bicep
        env:
          LOCAL_INPUT: ${SHARED_INPUT}
          LITERAL_INPUT: fixed-value
          EMPTY_INPUT: ""
        outputs:
          LOCAL_OUTPUT: SHARED_OUTPUT
    services:
      api:
        project: ./src/api
        host: containerapp
        language: js
`)
	require.NoError(t, err)
	require.Equal(t, "${SHARED_INPUT}", projectConfig.Layers[0].Infra[0].Env["LOCAL_INPUT"].Raw())
	require.Equal(t, "SHARED_OUTPUT", projectConfig.Layers[0].Infra[0].Outputs["LOCAL_OUTPUT"])

	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, path))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	yaml := string(contents)
	require.Contains(t, yaml, "/schemas/alpha/azure.yaml.json")
	require.Contains(t, yaml, "layers:")
	require.Contains(t, yaml, "- name: application")
	require.Contains(t, yaml, "infra:")
	require.Contains(t, yaml, "- provider: bicep")
	require.Contains(t, yaml, "env:")
	require.Contains(t, yaml, "outputs:")
	require.Contains(t, yaml, "${SHARED_INPUT}")
	require.Contains(t, yaml, "services:")
	require.Contains(t, yaml, "api:")
	require.NotContains(t, yaml, "layer: application")
	require.Equal(t, 1, strings.Count(yaml, "layers:"))

	reloaded, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, projectConfig.Layers[0].Infra[0].Env.Raw(), reloaded.Layers[0].Infra[0].Env.Raw())
	require.Equal(t, projectConfig.Layers[0].Infra[0].Outputs, reloaded.Layers[0].Infra[0].Outputs)
}

func TestSaveProjectLayersPreservesEmptyLayers(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{
		Name:   "layered-project",
		Layers: LayerConfigs{},
	}
	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, path))

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "layers: []")

	reloaded, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatLayersV2, reloaded.Format())
	require.Empty(t, reloaded.Layers)
}

func TestSaveProjectLayersRejectsMixedFormatsBeforeWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ProjectConfig)
	}{
		{
			name: "top-level services",
			mutate: func(config *ProjectConfig) {
				config.Services = map[string]*ServiceConfig{"api": {Name: "api"}}
			},
		},
		{
			name: "top-level infra",
			mutate: func(config *ProjectConfig) {
				config.Infra = provisioning.Options{Provider: provisioning.Bicep, Path: "infra"}
			},
		},
		{
			name: "top-level infra layers",
			mutate: func(config *ProjectConfig) {
				config.Infra = provisioning.Options{Layers: []provisioning.Options{
					{Name: "shared", Provider: provisioning.Bicep, Path: "infra"},
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "azure.yaml")
			projectConfig := &ProjectConfig{
				Name:   "layered-project",
				Layers: LayerConfigs{},
			}
			require.NoError(t, Save(t.Context(), projectConfig, path))

			before, err := os.ReadFile(path)
			require.NoError(t, err)
			test.mutate(projectConfig)

			err = Save(t.Context(), projectConfig, path)
			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func layerVariableDocument(format string, infra map[string]any) map[string]any {
	document := map[string]any{"name": "test-project"}
	if format == "infra" {
		document["infra"] = map[string]any{"layers": []any{infra}}
	} else {
		document["layers"] = []any{map[string]any{"name": "application", "infra": []any{infra}}}
	}
	return document
}

func loadLayerTestSchema(t *testing.T, version string) *jsonschema.Schema {
	t.Helper()

	rawSchema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "schemas", version, "azure.yaml.json"))
	require.NoError(t, err)
	var schemaDocument map[string]any
	require.NoError(t, json.Unmarshal(rawSchema, &schemaDocument))

	const resourceURI = "mem://azure.yaml.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, schemaDocument))
	repositoryRoot := filepath.Join("..", "..", "..", "..")
	jsonGlob := filepath.Join(repositoryRoot, "cli", "azd", "extensions", "*", "schemas", "*.json")
	extensionSchemas, err := filepath.Glob(jsonGlob)
	require.NoError(t, err)
	for _, schemaPath := range extensionSchemas {
		rawExtensionSchema, err := os.ReadFile(schemaPath)
		require.NoError(t, err)
		var extensionSchema map[string]any
		require.NoError(t, json.Unmarshal(rawExtensionSchema, &extensionSchema))
		relativePath, err := filepath.Rel(repositoryRoot, schemaPath)
		require.NoError(t, err)
		require.NoError(t, compiler.AddResource(
			"https://raw.githubusercontent.com/Azure/azure-dev/main/"+filepath.ToSlash(relativePath),
			extensionSchema,
		))
	}
	schema, err := compiler.Compile(resourceURI)
	require.NoError(t, err)
	return schema
}

func TestProjectLayersAlphaSchema(t *testing.T) {
	t.Parallel()

	schema := loadLayerTestSchema(t, "alpha")

	layer := map[string]any{
		"name": "application",
		"infra": []any{map[string]any{
			"name": "app-infra", "provider": "bicep", "path": "./infra/app",
		}},
		"services": map[string]any{
			"api": map[string]any{"host": "containerapp", "project": "./src/api"},
		},
	}
	require.NoError(t, schema.Validate(map[string]any{
		"name":   "layered-project",
		"layers": []any{layer},
	}))

	// bicep and terraform require a 'path' attribute
	for _, provider := range []string{"bicep", "terraform"} {
		require.Error(t, schema.Validate(map[string]any{
			"name": "layered-project",
			"layers": []any{map[string]any{
				"name": "application",
				"infra": []any{map[string]any{
					"name":     "app-infra",
					"provider": provider,
				}},
			}},
		}), provider)
	}

	// Custom providers don't. If they need a path, they can define one in their provider-specific config.
	require.NoError(t, schema.Validate(map[string]any{
		"name": "layered-project",
		"layers": []any{map[string]any{
			"name": "application",
			"infra": []any{map[string]any{
				"name":     "foundry",
				"provider": "microsoft.foundry",
				"outputs":  map[string]any{"LOCAL_OUTPUT": "PROJECT_OUTPUT"},
			}},
		}},
	}))
	require.Error(t, schema.Validate(map[string]any{
		"name": "layered-project",
		"layers": []any{map[string]any{
			"name": "application",
			"infra": []any{map[string]any{
				"name":     "foundry",
				"provider": "microsoft.foundry",
				"env":      map[string]any{"LOCAL_INPUT": "${PROJECT_INPUT}"},
			}},
		}},
	}))

	for _, test := range []struct {
		property string
		value    any
	}{
		{property: "infra", value: []any{}},
		{property: "services", value: map[string]any{}},
	} {
		projectDocument := map[string]any{
			"name": "layered-project",
			"layers": []any{map[string]any{
				"name":        "application",
				test.property: test.value,
			}},
		}
		require.Error(t, schema.Validate(projectDocument), test.property)
	}

	for _, incompatibleProperty := range []string{"infra", "resources", "services"} {
		projectDocument := map[string]any{
			"name":               "layered-project",
			"layers":             []any{layer},
			incompatibleProperty: map[string]any{},
		}
		require.Error(t, schema.Validate(projectDocument), incompatibleProperty)
	}
}

func TestLayerVariableSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		env      any
		outputs  any
		invalid  bool
	}{
		{
			name: "env expressions", provider: "bicep",
			env: map[string]any{
				"LOCAL_INPUT": "${PROJECT_INPUT}", "URL": "https://${PROJECT_HOST}/api",
				"DEFAULT": "${OPTIONAL:-fallback}", "CONTEXT": "${AZURE_LOCATION}",
				"CONTROL": "${AZD_CONTROL}", "LITERAL": "literal value", "EMPTY": "",
			},
			outputs: map[string]any{"LOCAL_OUTPUT": "PROJECT_OUTPUT"},
		},
		{
			name: "terraform expressions", provider: "terraform",
			env: map[string]any{"FIRST": "${SHARED}", "SECOND": "${SHARED}"},
		},
		{
			name:    "valid variable names",
			env:     map[string]any{"_name0": "literal", "a": ""},
			outputs: map[string]any{"_output0": "_project0", "b": "c"},
		},
		{
			name: "custom output only", provider: "microsoft.foundry",
			outputs: map[string]any{"LOCAL_OUTPUT": "PROJECT_OUTPUT"},
		},
		{
			name: "custom empty env", provider: "microsoft.foundry", env: map[string]any{},
			outputs: map[string]any{"LOCAL_OUTPUT": "PROJECT_OUTPUT"},
		},
		{
			name: "custom nonempty env", provider: "microsoft.foundry",
			env: map[string]any{"LOCAL_INPUT": "${PROJECT_INPUT}"}, invalid: true,
		},
		{
			name: "custom literal env", provider: "microsoft.foundry",
			env: map[string]any{"LOCAL_INPUT": "literal"}, invalid: true,
		},
		{
			name: "custom explicit empty value", provider: "microsoft.foundry",
			env: map[string]any{"LOCAL_INPUT": ""}, invalid: true,
		},
		{name: "env values must be strings", env: map[string]any{"LOCAL_INPUT": 42}, invalid: true},
		{name: "outputs cannot be expressions", outputs: map[string]any{"LOCAL_OUTPUT": "${OUTPUT}"}, invalid: true},
		{name: "output values must be strings", outputs: map[string]any{"LOCAL_OUTPUT": 42}, invalid: true},
	}

	for _, version := range []string{"v1.0", "alpha"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			schema := loadLayerTestSchema(t, version)
			formats := []string{"infra"}
			if version == "alpha" {
				formats = append(formats, "project")
			}
			for _, format := range formats {
				for _, test := range tests {
					t.Run(format+"/"+test.name, func(t *testing.T) {
						t.Parallel()
						provider := test.provider
						if provider == "" {
							provider = "bicep"
						}
						infra := map[string]any{
							"name": "component", "provider": provider, "path": "infra/component",
						}
						if test.env != nil {
							infra["env"] = test.env
						}
						if test.outputs != nil {
							infra["outputs"] = test.outputs
						}
						err := schema.Validate(layerVariableDocument(format, infra))
						if test.invalid {
							require.Error(t, err)
						} else {
							require.NoError(t, err)
						}
					})
				}
				for _, field := range []string{"env key", "output key", "output destination"} {
					for _, name := range []string{
						"", "BAD-NAME", "1BAD", "NAME\n",
						"AZURE_ENV_NAME", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_LOCATION",
						"AZURE_RESOURCE_GROUP", "AZURE_PRINCIPAL_ID", "AZURE_PRINCIPAL_TYPE",
						"AZD_CONTROL", "azure_subscription_id", "azd_control",
					} {
						t.Run(fmt.Sprintf("%s/%s/%q", format, field, name), func(t *testing.T) {
							t.Parallel()
							infra := map[string]any{
								"name": "component", "provider": "bicep", "path": "infra/component",
							}
							switch field {
							case "env key":
								infra["env"] = map[string]any{name: "literal"}
							case "output key":
								infra["outputs"] = map[string]any{name: "PROJECT_OUTPUT"}
							case "output destination":
								infra["outputs"] = map[string]any{"LOCAL_OUTPUT": name}
							}
							require.Error(t, schema.Validate(layerVariableDocument(format, infra)))
						})
					}
				}
			}
			for _, field := range []string{"env", "outputs"} {
				t.Run("root/"+field, func(t *testing.T) {
					t.Parallel()
					require.Error(t, schema.Validate(map[string]any{
						"name": "test-project",
						"infra": map[string]any{
							"provider": "bicep", field: map[string]any{"LOCAL_VARIABLE": "PROJECT_VARIABLE"},
						},
					}))
				})
			}
		})
	}
}

func TestProjectFormatPreservesExplicitEmptyLayerCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *ProjectConfig
		want   ProjectFormat
	}{
		{name: "flat", config: &ProjectConfig{}, want: ProjectFormatFlat},
		{name: "empty infra layers", config: &ProjectConfig{
			Infra: provisioning.Options{Layers: []provisioning.Options{}},
		}, want: ProjectFormatInfraV1},
		{name: "empty project layers", config: &ProjectConfig{
			Layers: LayerConfigs{},
		}, want: ProjectFormatLayersV2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, test.config.Format())
		})
	}
}

func TestExplicitEmptyInfraLayersUsesV1Format(t *testing.T) {
	t.Parallel()

	projectConfig := map[string]any{
		"name": "test-project",
		"infra": map[string]any{
			"path":   "infra",
			"layers": []any{},
		},
	}
	projectYaml, err := yaml.Marshal(projectConfig)
	require.NoError(t, err)
	require.Contains(t, string(projectYaml), "layers: []")

	config, err := Parse(t.Context(), string(projectYaml))
	require.NoError(t, err)

	// The presence of infra.layers identifies the legacy v1 format, even when the list is empty.
	require.Equal(t, ProjectFormatInfraV1, config.Format())
	require.Empty(t, config.Infra.Layers)

	// A zero-length legacy layer list falls back to the root infra entry.
	entries := config.InfrastructureConfigs()
	require.Len(t, entries, 1)
	require.Equal(t, "infra", entries[0].Path)
}

func TestProjectConfigAccessorsPreserveNonV2Formats(t *testing.T) {
	t.Parallel()

	service := &ServiceConfig{Name: "api"}
	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{"api": service},
		Infra: provisioning.Options{
			Provider: provisioning.Bicep,
			Layers: []provisioning.Options{
				{Name: "network", Provider: provisioning.Terraform},
				{Name: "application", Provider: provisioning.Bicep},
			},
		},
	}

	require.Equal(t, ProjectFormatInfraV1, projectConfig.Format())
	require.Same(t, service, projectConfig.ServiceConfigs()["api"])
	require.Equal(t, projectConfig.Infra.Layers, projectConfig.InfrastructureConfigs())
}

func TestSaveProjectInfraV1PreservesFormat(t *testing.T) {
	t.Parallel()

	const projectYaml = `name: test-project
infra:
  provider: bicep
  layers:
    - name: network
      path: infra/network
      module: network
      outputs:
        LOCAL_NETWORK_ID: PROJECT_NETWORK_ID
    - name: application
      provider: terraform
      path: infra/application
      env:
        LOCAL_NETWORK_ID: ${PROJECT_NETWORK_ID}
        LITERAL_INPUT: fixed-value
        EMPTY_INPUT: ""
services:
  api:
    host: appservice
    language: python
    project: src/api
`

	projectConfig, err := Parse(t.Context(), projectYaml)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, projectConfig.Format())

	projectFile := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, projectFile))

	rawConfig, err := LoadConfig(t.Context(), projectFile)
	require.NoError(t, err)
	require.NoError(t, rawConfig.Set("metadata.compatibilityTest", true))
	require.NoError(t, SaveConfig(t.Context(), rawConfig, projectFile))

	reloaded, err := Load(t.Context(), projectFile)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, reloaded.Format())
	require.Equal(t, provisioning.Bicep, reloaded.Infra.Provider)
	require.Len(t, reloaded.Infra.Layers, 2)
	require.Equal(t, "network", reloaded.Infra.Layers[0].Name)
	require.Equal(t, provisioning.Terraform, reloaded.Infra.Layers[1].Provider)
	require.Equal(t, projectConfig.Infra.Layers[0].Outputs, reloaded.Infra.Layers[0].Outputs)
	require.Equal(t, projectConfig.Infra.Layers[1].Env.Raw(), reloaded.Infra.Layers[1].Env.Raw())
	require.Contains(t, reloaded.Services, "api")

	contents, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	require.Contains(t, string(contents), "schemas/v1.0/azure.yaml.json")
	require.NotContains(t, string(contents), "\nlayers:")
}

func TestValidateLayerGraph_AcceptsV2Project(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{
			Name: "foundry",
			Infra: []provisioning.Options{
				{Name: "foundry-account", Provider: provisioning.Bicep},
				{Name: "foundry-project", Provider: "microsoft.foundry", DependsOn: []string{"foundry-account"}},
			},
			Services: map[string]*ServiceConfig{"ai-project": {Name: "ai-project"}},
		},
		{
			Name: "agents",
			Infra: []provisioning.Options{
				{Name: "agent-resources", Provider: provisioning.Bicep, DependsOn: []string{"foundry-project"}},
			},
			Services: map[string]*ServiceConfig{
				"writer-agent": {Name: "writer-agent", Uses: []string{"ai-project"}},
			},
		},
	}}

	require.NoError(t, ValidateLayerGraph(projectConfig))
}

func TestValidateLayerGraph_RejectsLayerCycle(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "a", Infra: []provisioning.Options{{Name: "a-infra", DependsOn: []string{"b-infra"}}}},
		{Name: "b", Infra: []provisioning.Options{{Name: "b-infra", DependsOn: []string{"a-infra"}}}},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "circular dependency")
}

func TestValidateLayerGraph_AcceptsAcyclicEntriesAcrossLayers(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "a", Infra: []provisioning.Options{
			{Name: "a1"},
			{Name: "a2", DependsOn: []string{"b1"}},
		}},
		{Name: "b", Infra: []provisioning.Options{
			{Name: "b1"},
			{Name: "b2", DependsOn: []string{"a1"}},
		}},
	}}

	require.NoError(t, ValidateLayerGraph(projectConfig))
}

func TestValidateLayerGraph_RejectsIntraLayerInfrastructureCycle(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{
			Name: "application",
			Infra: []provisioning.Options{
				{Name: "api", DependsOn: []string{"worker"}},
				{Name: "worker", DependsOn: []string{"api"}},
			},
		},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "circular dependency detected at infrastructure layer")
}

func TestValidateLayerGraph_RejectsUnknownInfraDependency(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "application", Infra: []provisioning.Options{{Name: "application", DependsOn: []string{"missing"}}}},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "depends on unknown infrastructure layer")
}
