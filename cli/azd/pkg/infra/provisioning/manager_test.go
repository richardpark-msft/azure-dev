// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestProvisionInitializesEnvironment(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Select an Azure Subscription to use")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Select an Azure location")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})

	envManager := registerContainerDependencies(mockContext, env)
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	require.Equal(t, "00000000-0000-0000-0000-000000000000", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
}

func TestManagerPreview(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	envManager := registerContainerDependencies(mockContext, env)
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	deploymentPlan, err := mgr.Preview(*mockContext.Context)

	require.NotNil(t, deploymentPlan)
	require.Nil(t, err)
}

func TestManagerGetState(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	envManager := registerContainerDependencies(mockContext, env)
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	getResult, err := mgr.State(*mockContext.Context, nil)

	require.NotNil(t, getResult)
	require.Nil(t, err)
}

func TestManagerDeploy(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	envManager := registerContainerDependencies(mockContext, env)
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NotNil(t, deployResult)
	require.Nil(t, err)
}

func TestManagerDestroyWithPositiveConfirmation(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(true)

	envManager := registerContainerDependencies(mockContext, env)

	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	destroyOptions := provisioning.NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, destroyOptions)

	require.NotNil(t, destroyResult)
	require.Nil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

func TestManagerDestroyWithNegativeConfirmation(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
	})

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(false)

	envManager := registerContainerDependencies(mockContext, env)
	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	destroyOptions := provisioning.NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, destroyOptions)

	require.Nil(t, destroyResult)
	require.NotNil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

type destroyResultProvider struct {
	provisioning.Provider
	result *provisioning.DestroyResult
}

type mappedEnvironmentProvider struct {
	provisioning.Provider
	env              environment.ScopedEnvironment
	initializedInput string
}

type variableTestProvider struct {
	provisioning.Provider
	options    provisioning.Options
	parameters []provisioning.Parameter
	outputs    []provisioning.PlannedOutput
	state      *provisioning.StateResult
	deployment *provisioning.Deployment
	err        error
}

func (p *variableTestProvider) Initialize(_ context.Context, _ string, options provisioning.Options) error {
	p.options = options
	return nil
}

func (p *variableTestProvider) Parameters(context.Context) ([]provisioning.Parameter, error) {
	return p.parameters, p.err
}

func (p *variableTestProvider) PlannedOutputs(context.Context) ([]provisioning.PlannedOutput, error) {
	return p.outputs, p.err
}

func (p *variableTestProvider) State(
	context.Context, *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return p.state, p.err
}

func (p *variableTestProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	return &provisioning.DeployResult{Deployment: p.deployment}, p.err
}

type providerScopeMarker int

type localInputOperationsProvider struct {
	variableTestProvider
	env environment.ScopedEnvironment
}

func (p *localInputOperationsProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	p.env.DotenvSet("LOCAL_PATH", "provider-local-path")
	p.env.DotenvDelete("REMOVED_INPUT")
	return p.variableTestProvider.Deploy(ctx)
}

type operationsUploadRecorder struct {
	calls        int
	subscription string
	shareURL     string
	source       string
}

func (r *operationsUploadRecorder) UploadPath(
	_ context.Context, subscription, shareURL, source string,
) error {
	r.calls++
	r.subscription, r.shareURL, r.source = subscription, shareURL, source
	return nil
}

func writeOperationsFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azd.operations.yaml"), []byte(`operations:
  - type: FileShareUpload
    config:
      storageAccount: ${LOCAL_STORAGE}
      fileShareName: ${LOCAL_SHARE}${REMOVED_INPUT}
      path: ${LOCAL_PATH}
`), 0600))
	return dir
}

func TestManagerOperations_UsesLocalInputsAndRawOutputs(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_AZD_OPERATIONS", "true")
	t.Setenv("REMOVED_INPUT", "ambient-value-must-not-return")

	mockContext := mocks.NewMockContext(t.Context())
	projectEnv := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "shared-subscription",
		"LOCAL_STORAGE":                      "unrelated-account",
		"PROJECT_STORAGE":                    "old-account",
		"LOCAL_SHARE":                        "shared-share",
		"LOCAL_PATH":                         "shared-path",
		"REMOVED_INPUT":                      "backing-value-must-not-return",
		"PROJECT_SHARE":                      "resolved-share",
	})
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment { return projectEnv })
	var provider *localInputOperationsProvider
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &localInputOperationsProvider{env: env, variableTestProvider: variableTestProvider{
				deployment: &provisioning.Deployment{Outputs: map[string]provisioning.OutputParameter{
					"LOCAL_STORAGE": {Type: provisioning.ParameterTypeString, Value: "returned-account"},
				}},
			}}
			return provider
		})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, projectEnv).Return(nil).Once()
	uploads := &operationsUploadRecorder{}
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, envManager, projectEnv,
		mockContext.Console, mockContext.AlphaFeaturesManager, uploads, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep, Path: writeOperationsFile(t),
		Env: osutil.ExpandableMap{
			"LOCAL_STORAGE": osutil.NewExpandableString("input-account"),
			"LOCAL_SHARE":   osutil.NewExpandableString("${PROJECT_SHARE}"),
			"LOCAL_PATH":    osutil.NewExpandableString("initial-local-path"),
			"REMOVED_INPUT": osutil.NewExpandableString("removed-during-deploy"),
		},
		Outputs: map[string]string{"LOCAL_STORAGE": "PROJECT_STORAGE"},
	}))
	projectEnv.DotenvSet("PROJECT_SHARE", "changed-after-initialize")

	result, err := manager.Deploy(t.Context())
	require.NoError(t, err)

	require.Equal(t, &operationsUploadRecorder{
		calls: 1, subscription: "shared-subscription",
		shareURL: "https://returned-account.file.core.windows.net/resolved-share",
		source:   "provider-local-path",
	}, uploads)

	require.Equal(t, "input-account", provider.env.Getenv("LOCAL_STORAGE"),
		"operations must not replace the provider's input with its returned output")
	require.Equal(t, "resolved-share", provider.env.Getenv("LOCAL_SHARE"),
		"input expressions must not be re-evaluated after initialization")
	require.Equal(t, "provider-local-path", provider.env.Getenv("LOCAL_PATH"))
	_, present := provider.env.LookupEnv("REMOVED_INPUT")
	require.False(t, present)

	require.Equal(t, "returned-account", projectEnv.Getenv("PROJECT_STORAGE"))
	require.Equal(t, "unrelated-account", projectEnv.Getenv("LOCAL_STORAGE"))
	require.Equal(t, "shared-share", projectEnv.Getenv("LOCAL_SHARE"))
	require.Equal(t, "shared-path", projectEnv.Getenv("LOCAL_PATH"))
	require.Equal(t, "backing-value-must-not-return", projectEnv.Getenv("REMOVED_INPUT"))
	require.Contains(t, result.Deployment.Outputs, "LOCAL_STORAGE")
	require.NotContains(t, result.Deployment.Outputs, "PROJECT_STORAGE")
	envManager.AssertExpectations(t)
}

func TestManagerOperations_UsesStagedOutputs(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_AZD_OPERATIONS", "true")
	t.Setenv("REMOVED_INPUT", "ambient-value-must-not-return")

	mockContext := mocks.NewMockContext(t.Context())
	projectEnv := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "shared-subscription",
		"LOCAL_STORAGE":                      "unrelated-account",
		"PROJECT_STORAGE":                    "old-account",
		"LOCAL_SHARE":                        "shared-share",
		"LOCAL_PATH":                         "shared-path",
		"REMOVED_INPUT":                      "backing-value-must-not-return",
		"PROJECT_SHARE":                      "snapshot-share",
	})
	stagedEnv := environment.NewWithValues(projectEnv.Name(), projectEnv.Dotenv())
	projectEnv.DotenvSet("PROJECT_SHARE", "live-share")

	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment { return projectEnv })
	var provider *localInputOperationsProvider
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &localInputOperationsProvider{env: env, variableTestProvider: variableTestProvider{
				deployment: &provisioning.Deployment{Outputs: map[string]provisioning.OutputParameter{
					"LOCAL_STORAGE": {Type: provisioning.ParameterTypeString, Value: "returned-account"},
				}},
			}}
			return provider
		})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, stagedEnv).Return(nil).Once()
	uploads := &operationsUploadRecorder{}
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, envManager, stagedEnv,
		mockContext.Console, mockContext.AlphaFeaturesManager, uploads, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep, Path: writeOperationsFile(t),
		Env: osutil.ExpandableMap{
			"LOCAL_STORAGE": osutil.NewExpandableString("input-account"),
			"LOCAL_SHARE":   osutil.NewExpandableString("${PROJECT_SHARE}"),
			"LOCAL_PATH":    osutil.NewExpandableString("initial-local-path"),
			"REMOVED_INPUT": osutil.NewExpandableString("removed-during-deploy"),
		},
		Outputs: map[string]string{"LOCAL_STORAGE": "PROJECT_STORAGE"},
	}))

	result, err := manager.Deploy(t.Context())
	require.NoError(t, err)

	require.Same(t, projectEnv, provider.env.BackingEnv())
	require.Equal(t, "live-share", provider.env.Getenv("LOCAL_SHARE"),
		"provider inputs must resolve from the live environment, not the output-staging snapshot")
	require.Equal(t, &operationsUploadRecorder{
		calls: 1, subscription: "shared-subscription",
		shareURL: "https://returned-account.file.core.windows.net/live-share",
		source:   "provider-local-path",
	}, uploads)

	require.Equal(t, "returned-account", stagedEnv.Getenv("PROJECT_STORAGE"))
	require.Equal(t, "old-account", projectEnv.Getenv("PROJECT_STORAGE"))
	require.Equal(t, "unrelated-account", stagedEnv.Getenv("LOCAL_STORAGE"))
	require.Equal(t, "unrelated-account", projectEnv.Getenv("LOCAL_STORAGE"))
	require.Equal(t, "shared-path", stagedEnv.Getenv("LOCAL_PATH"))
	require.Equal(t, "shared-path", projectEnv.Getenv("LOCAL_PATH"))
	require.Equal(t, "backing-value-must-not-return", stagedEnv.Getenv("REMOVED_INPUT"))
	require.Equal(t, "backing-value-must-not-return", projectEnv.Getenv("REMOVED_INPUT"))
	require.Contains(t, result.Deployment.Outputs, "LOCAL_STORAGE")
	require.NotContains(t, result.Deployment.Outputs, "PROJECT_STORAGE")
	envManager.AssertExpectations(t)
}

func TestManagerOperations_CustomProviderMapsOutputs(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_AZD_OPERATIONS", "true")

	mockContext := mocks.NewMockContext(t.Context())
	projectEnv := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "shared-subscription",
		"LOCAL_STORAGE":                      "unrelated-account",
		"PROJECT_STORAGE":                    "old-account",
		"LOCAL_SHARE":                        "shared-share",
		"LOCAL_PATH":                         "shared-path",
		"REMOVED_INPUT":                      "",
	})
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment { return projectEnv })
	var providerEnv environment.ScopedEnvironment
	mockContext.Container.MustRegisterNamedTransient("custom.operations",
		func(env environment.ScopedEnvironment) provisioning.Provider {
			providerEnv = env
			return &variableTestProvider{deployment: &provisioning.Deployment{
				Outputs: map[string]provisioning.OutputParameter{
					"LOCAL_STORAGE": {Type: provisioning.ParameterTypeString, Value: "returned-account"},
				},
			}}
		})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, projectEnv).Return(nil).Once()
	uploads := &operationsUploadRecorder{}
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, envManager, projectEnv,
		mockContext.Console, mockContext.AlphaFeaturesManager, uploads, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: "custom.operations", Path: writeOperationsFile(t),
		Outputs: map[string]string{"LOCAL_STORAGE": "PROJECT_STORAGE"},
	}))
	require.Same(t, projectEnv, providerEnv, "output-only providers must not receive an input overlay")

	result, err := manager.Deploy(t.Context())
	require.NoError(t, err)

	require.Equal(t, &operationsUploadRecorder{
		calls: 1, subscription: "shared-subscription",
		shareURL: "https://returned-account.file.core.windows.net/shared-share",
		source:   "shared-path",
	}, uploads)
	require.Equal(t, "returned-account", projectEnv.Getenv("PROJECT_STORAGE"))
	require.Equal(t, "unrelated-account", providerEnv.Getenv("LOCAL_STORAGE"),
		"publishing an output must not redirect provider reads")
	require.Equal(t, "shared-share", projectEnv.Getenv("LOCAL_SHARE"))
	require.Equal(t, "shared-path", projectEnv.Getenv("LOCAL_PATH"))
	require.Contains(t, result.Deployment.Outputs, "LOCAL_STORAGE")
	require.NotContains(t, result.Deployment.Outputs, "PROJECT_STORAGE")
	envManager.AssertExpectations(t)
}

func TestManagerRejectsPromptedEnvInputsForCI(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{"${PROJECT_INPUT}", "prefix-${FIRST}-${SECOND}", "literal"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(t.Context())
			provider := &variableTestProvider{parameters: []provisioning.Parameter{{
				Name: "requiredInput", Value: "prompted-value-must-not-be-disclosed",
				EnvVarMapping: []string{"LOCAL_INPUT"}, LocalPrompt: true,
			}}}
			mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
				return provider
			})
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, nil, environment.New("test"),
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)
			require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
				Provider: provisioning.Bicep,
				Env:      osutil.ExpandableMap{"LOCAL_INPUT": osutil.NewExpandableString(expression)},
			}))

			parameters, err := manager.Parameters(t.Context())
			require.ErrorContains(t, err, "requiredInput")
			require.ErrorContains(t, err, "local prompt")
			require.ErrorContains(t, err, "CI")
			require.ErrorContains(t, err, "env")
			require.NotContains(t, err.Error(), "prompted-value-must-not-be-disclosed")
			require.Nil(t, parameters)
			require.True(t, provider.parameters[0].LocalPrompt)
			require.Equal(t, "prompted-value-must-not-be-disclosed", provider.parameters[0].Value)
		})
	}
}

type providerScopeProbe struct {
	provisioning.Provider
	env     environment.ScopedEnvironment
	console input.Console
	marker  *providerScopeMarker
}

func (*providerScopeProbe) Initialize(context.Context, string, provisioning.Options) error {
	return nil
}

func TestManagerKeepsPromptedParametersWithoutEnvOverrides(t *testing.T) {
	t.Parallel()

	provider := &variableTestProvider{parameters: []provisioning.Parameter{{
		Name: "ordinaryInput", Value: "prompted-value",
		EnvVarMapping: []string{"ORDINARY_INPUT"}, LocalPrompt: true,
	}}}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Env:      osutil.ExpandableMap{"OTHER_INPUT": osutil.NewExpandableString("literal")},
	}))

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.parameters, parameters)
}

func TestUpdateEnvironmentSerializesOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  provisioning.OutputParameter
		want    string
		wantErr bool
	}{
		{name: "string", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeString, Value: "value"}, want: "value"},
		{name: "number", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeNumber, Value: 42}, want: "42"},
		{name: "boolean", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeBoolean, Value: true}, want: "true"},
		{name: "array", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeArray, Value: []any{"a", 1}}, want: `["a",1]`},
		{name: "object", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeObject, Value: map[string]any{"key": "value"}}, want: `{"key":"value"}`},
		{name: "invalid object", output: provisioning.OutputParameter{
			Type: provisioning.ParameterTypeObject, Value: make(chan int)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := environment.NewWithValues("test", map[string]string{"OUTPUT": "original"})
			manager := &mockenv.MockEnvManager{}
			if !tt.wantErr {
				manager.On("Save", mock.Anything, env).Return(nil).Once()
			}
			err := provisioning.UpdateEnvironment(t.Context(), map[string]provisioning.OutputParameter{
				"OUTPUT": tt.output,
			}, env, manager)
			if tt.wantErr {
				require.ErrorContains(t, err, "invalid value for output parameter 'OUTPUT'")
				require.Equal(t, "original", env.Getenv("OUTPUT"))
				manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, env.Getenv("OUTPUT"))
			}
			manager.AssertExpectations(t)
		})
	}
}

func TestManagerProviderOverlayPreservesCommandScope(t *testing.T) {
	t.Parallel()

	for _, inputs := range []bool{false, true} {
		t.Run(fmt.Sprintf("inputs=%t", inputs), func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			backing := environment.NewWithValues("test", map[string]string{"PROJECT_INPUT": "value"})
			mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
				return backing
			})
			mockContext.Container.MustRegisterScoped(func() *providerScopeMarker {
				return new(providerScopeMarker)
			})

			var commandMarker *providerScopeMarker
			require.NoError(t, mockContext.Container.Resolve(&commandMarker))

			var provider *providerScopeProbe
			mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep), func(
				env environment.ScopedEnvironment,
				console input.Console,
				marker *providerScopeMarker,
			) provisioning.Provider {
				provider = &providerScopeProbe{env: env, console: console, marker: marker}
				return provider
			})

			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, nil, backing,
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)
			options := provisioning.Options{Provider: provisioning.Bicep}
			if inputs {
				options.Env = osutil.ExpandableMap{"PROVIDER_INPUT": osutil.NewExpandableString("${PROJECT_INPUT}")}
			}

			require.NoError(t, manager.Initialize(t.Context(), "", options))
			require.Same(t, commandMarker, provider.marker)
			require.Same(t, mockContext.Console, provider.console)
			require.Same(t, backing, provider.env.BackingEnv())
			if inputs {
				require.Equal(t, "value", provider.env.Getenv("PROVIDER_INPUT"))
			} else {
				require.Same(t, backing, provider.env)
			}
		})
	}
}

func TestManagerPipelineMetadataUsesProjectView(t *testing.T) {
	t.Parallel()

	provider := &variableTestProvider{
		parameters: []provisioning.Parameter{
			{Name: "plain", Value: "value", EnvVarMapping: []string{"PROVIDER_VARIABLE", "UNCHANGED"},
				UsingEnvVarMapping: true},
			{Name: "secret", Secret: true, Value: "secret", EnvVarMapping: []string{"PROVIDER_VARIABLE"},
				UsingEnvVarMapping: true},
			{Name: "overlapping", EnvVarMapping: []string{"PROJECT_VARIABLE"}},
			{Name: "unmapped"},
		},
		outputs: []provisioning.PlannedOutput{{Name: "PROVIDER_VARIABLE"}, {Name: "PROJECT_VARIABLE"}, {Name: "UNCHANGED"}},
	}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Env: osutil.ExpandableMap{
			"PROVIDER_VARIABLE": osutil.NewExpandableString("${PROJECT_VARIABLE}"),
			"PROJECT_VARIABLE":  osutil.NewExpandableString("${OTHER_PROJECT_VARIABLE}"),
		},
		Outputs: map[string]string{
			"PROVIDER_VARIABLE": "PROJECT_VARIABLE",
			"PROJECT_VARIABLE":  "OTHER_PROJECT_VARIABLE",
		},
	}))

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{
		{Name: "plain", EnvVarMapping: []string{"PROJECT_VARIABLE", "UNCHANGED"},
			UsingEnvVarMapping: true},
		{Name: "secret", Secret: true, EnvVarMapping: []string{"PROJECT_VARIABLE"}, UsingEnvVarMapping: true},
		{Name: "overlapping", EnvVarMapping: []string{"OTHER_PROJECT_VARIABLE"}},
		{Name: "unmapped"},
	}, parameters)

	outputs, err := manager.PlannedOutputs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.PlannedOutput{
		{Name: "PROJECT_VARIABLE"}, {Name: "OTHER_PROJECT_VARIABLE"}, {Name: "UNCHANGED"},
	}, outputs)
	require.Equal(t, []string{"PROVIDER_VARIABLE", "UNCHANGED"}, provider.parameters[0].EnvVarMapping)
	require.Equal(t, "PROVIDER_VARIABLE", provider.outputs[0].Name)

	provider.err = errors.New("provider metadata failed")
	_, err = manager.Parameters(t.Context())
	require.ErrorIs(t, err, provider.err)
	_, err = manager.PlannedOutputs(t.Context())
	require.ErrorIs(t, err, provider.err)

	provider.err = nil
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{Provider: provisioning.Bicep}))
	parameters, err = manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.parameters, parameters)
	outputs, err = manager.PlannedOutputs(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.outputs, outputs)
}

func TestManagerStateUsesProjectView(t *testing.T) {
	t.Parallel()

	providerState := &provisioning.StateResult{State: &provisioning.State{
		Outputs: map[string]provisioning.OutputParameter{
			"FIRST":  {Type: provisioning.ParameterTypeString, Value: "one"},
			"SECOND": {Type: provisioning.ParameterTypeString, Value: "two"},
		},
		Resources: []provisioning.Resource{{Id: "resource-id"}},
	}}
	provider := &variableTestProvider{state: providerState}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)

	err := manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Outputs:  map[string]string{"FIRST": "SECOND", "SECOND": "SHARED"},
	})
	require.NoError(t, err)

	result, err := manager.State(t.Context(), nil)

	require.NoError(t, err)
	require.Equal(t, map[string]provisioning.OutputParameter{
		"SECOND": {Type: provisioning.ParameterTypeString, Value: "one"},
		"SHARED": {Type: provisioning.ParameterTypeString, Value: "two"},
	}, result.State.Outputs)
	require.Equal(t, providerState.State.Resources, result.State.Resources)
	require.Equal(t, map[string]provisioning.OutputParameter{
		"FIRST":  {Type: provisioning.ParameterTypeString, Value: "one"},
		"SECOND": {Type: provisioning.ParameterTypeString, Value: "two"},
	}, providerState.State.Outputs)
}

func TestManagerRejectsEnvForCustomProviders(t *testing.T) {
	t.Parallel()

	const customProvider = provisioning.ProviderKind("contoso.custom")
	tests := []struct {
		name            string
		provider        provisioning.ProviderKind
		defaultProvider provisioning.DefaultProviderResolver
	}{
		{name: "explicit", provider: customProvider, defaultProvider: defaultProvider},
		{
			name:     "resolved default",
			provider: provisioning.NotSpecified,
			defaultProvider: func() (provisioning.ProviderKind, error) {
				return customProvider, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			manager := provisioning.NewManager(
				mockContext.Container, tt.defaultProvider, nil, environment.New("test"),
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)

			err := manager.Initialize(t.Context(), "", provisioning.Options{
				Provider: tt.provider,
				Env:      osutil.ExpandableMap{"LOCAL_INPUT": osutil.NewExpandableString("${PROJECT_INPUT}")},
			})

			require.ErrorContains(t, err, `provider "contoso.custom" is not supported`)
		})
	}
}

func TestManagerCustomProvidersSupportOutputsWithoutInputEnvironment(t *testing.T) {
	t.Parallel()

	const kind = provisioning.ProviderKind("contoso.custom")
	backing := environment.NewWithValues("test", map[string]string{"LOCAL_OUTPUT": "existing"})
	provider := &variableTestProvider{
		state: &provisioning.StateResult{State: &provisioning.State{
			Outputs: map[string]provisioning.OutputParameter{
				"LOCAL_OUTPUT": {Type: provisioning.ParameterTypeString, Value: "returned"},
			},
		}},
		parameters: []provisioning.Parameter{{
			Name: "name", Value: "input-value", EnvVarMapping: []string{"LOCAL_OUTPUT"}, UsingEnvVarMapping: true,
		}},
	}
	provider.deployment = &provisioning.Deployment{Outputs: provider.state.State.Outputs}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(kind), func() provisioning.Provider { return provider })
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil).Once()
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, envManager, backing,
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: kind, Outputs: map[string]string{"LOCAL_OUTPUT": "PROJECT_OUTPUT"},
	}))

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, provider.parameters, parameters, "output mappings must not rename input metadata")
	state, err := manager.State(t.Context(), nil)
	require.NoError(t, err)
	require.Contains(t, state.State.Outputs, "PROJECT_OUTPUT")
	_, err = manager.Deploy(t.Context())
	require.NoError(t, err)
	require.Equal(t, "returned", backing.Getenv("PROJECT_OUTPUT"))
	require.Equal(t, "existing", backing.Getenv("LOCAL_OUTPUT"))
	envManager.AssertExpectations(t)
}

func TestManagerResolvesEnvOnceWithoutPersistingInputs(t *testing.T) {
	t.Setenv("PROCESS_SOURCE", "from-process")
	t.Setenv("TEST_SOURCE_NOT_SET", "")
	t.Setenv("MISSING_INPUT", "must-not-fall-back")
	mockContext := mocks.NewMockContext(t.Context())
	backing := environment.NewWithValues("test", map[string]string{
		"HOST": "original-host", "NAME": "orders", "LOCAL_INPUT": "original-local",
		"SOURCE": "source-value", "CHAIN": "project-chain",
		"UNEXPANDED_SOURCE": "${HOST}",
	})
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment { return backing })
	var provider *mappedEnvironmentProvider
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &mappedEnvironmentProvider{env: env}
			return provider
		})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, backing,
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Env: osutil.ExpandableMap{
			"LOCAL_INPUT":   osutil.NewExpandableString("Server=${HOST};Database=${NAME}"),
			"DUPLICATE":     osutil.NewExpandableString("${HOST}"),
			"CHAIN":         osutil.NewExpandableString("${SOURCE}"),
			"NO_CHAIN":      osutil.NewExpandableString("${CHAIN}"),
			"MISSING":       osutil.NewExpandableString("${TEST_SOURCE_NOT_SET:-default}"),
			"MISSING_INPUT": osutil.NewExpandableString("${TEST_SOURCE_NOT_SET}"),
			"EMPTY":         osutil.NewExpandableString(""),
			"PROCESS":       osutil.NewExpandableString("${PROCESS_SOURCE}"),
			"LITERAL":       osutil.NewExpandableString("literal"),
			"NO_RECURSE":    osutil.NewExpandableString("${UNEXPANDED_SOURCE}"),
		},
	}))
	env := provider.env
	require.Equal(t, "Server=original-host;Database=orders", provider.initializedInput)
	require.Equal(t, "original-host", env.Getenv("DUPLICATE"))
	require.Equal(t, "source-value", env.Getenv("CHAIN"))
	require.Equal(t, "project-chain", env.Getenv("NO_CHAIN"), "env entries do not expand through each other")
	require.Equal(t, "default", env.Getenv("MISSING"))
	missingValue, missingPresent := env.LookupEnv("MISSING_INPUT")
	require.True(t, missingPresent)
	require.Empty(t, missingValue)
	require.Equal(t, "from-process", env.Getenv("PROCESS"))
	require.Equal(t, "literal", env.Getenv("LITERAL"))
	require.Equal(t, "${HOST}", env.Getenv("NO_RECURSE"))
	value, present := env.LookupEnv("EMPTY")
	require.True(t, present)
	require.Empty(t, value)

	backing.DotenvSet("HOST", "new-host")
	require.Equal(t, "Server=original-host;Database=orders", env.Getenv("LOCAL_INPUT"))
	env.DotenvSet("LOCAL_INPUT", "local-write")
	require.Equal(t, "local-write", env.Getenv("LOCAL_INPUT"))
	require.Equal(t, "original-local", backing.Getenv("LOCAL_INPUT"))
	require.Equal(t, "new-host", backing.Getenv("HOST"))
	env.DotenvDelete("LOCAL_INPUT")
	_, present = env.LookupEnv("LOCAL_INPUT")
	require.False(t, present)
	require.Equal(t, "original-local", backing.Getenv("LOCAL_INPUT"))
}

func TestManagerPipelineExpressionsExportSources(t *testing.T) {
	t.Parallel()

	provider := &variableTestProvider{parameters: []provisioning.Parameter{
		{Name: "connection", Value: "Server=host;Database=orders", Secret: true,
			EnvVarMapping: []string{"CONNECTION"}, UsingEnvVarMapping: true},
		{Name: "literal", Value: "fixed", EnvVarMapping: []string{"LITERAL"}, UsingEnvVarMapping: true},
		{Name: "default", Value: "fallback", EnvVarMapping: []string{"DEFAULTED"}, UsingEnvVarMapping: true},
	}}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Bicep,
		Env: osutil.ExpandableMap{
			"CONNECTION": osutil.NewExpandableString("Server=${HOST};Database=${DB}"),
			"LITERAL":    osutil.NewExpandableString("fixed"),
			"DEFAULTED":  osutil.NewExpandableString("${PRIMARY:-${FALLBACK}}"),
		},
	}))
	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"DB", "HOST"}, parameters[0].EnvVarMapping)
	require.Nil(t, parameters[0].Value)
	require.True(t, parameters[0].Secret)
	require.Empty(t, parameters[1].EnvVarMapping)
	require.Nil(t, parameters[1].Value)
	require.Equal(t, []string{"FALLBACK", "PRIMARY"}, parameters[2].EnvVarMapping)
	require.Equal(t, "Server=host;Database=orders", provider.parameters[0].Value)
}

func TestManagerEnvSourcesWithoutProviderMetadata(t *testing.T) {
	t.Parallel()

	provider := &variableTestProvider{}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Terraform), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Terraform,
		Env: osutil.ExpandableMap{
			"CONNECTION": osutil.NewExpandableString("${HOST}/${DATABASE}"),
			"LITERAL":    osutil.NewExpandableString("literal"),
		},
	}))

	parameters, err := manager.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.Parameter{{
		Name: "env.CONNECTION", Secret: true, EnvVarMapping: []string{"DATABASE", "HOST"}, UsingEnvVarMapping: true,
	}}, parameters)
}

func TestManagerPipelineDoesNotExportEarlierLayerOutputs(t *testing.T) {
	t.Parallel()

	for _, withMetadata := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadata=%t", withMetadata), func(t *testing.T) {
			t.Parallel()
			provider := &variableTestProvider{}
			if withMetadata {
				provider.parameters = []provisioning.Parameter{{
					Name: "input", EnvVarMapping: []string{"LOCAL_INPUT"},
				}}
			}
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
				return provider
			})
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, nil, environment.New("test"),
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)
			require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
				Provider:   provisioning.Bicep,
				VirtualEnv: map[string]string{"EARLIER_OUTPUT": ""},
				Env: osutil.ExpandableMap{
					"LOCAL_INPUT": osutil.NewExpandableString("${EARLIER_OUTPUT}/${CONFIG_INPUT}"),
				},
			}))
			require.Contains(t, provider.options.VirtualEnv, "LOCAL_INPUT")
			parameters, err := manager.Parameters(t.Context())
			require.NoError(t, err)
			require.Len(t, parameters, 1)
			require.Equal(t, []string{"CONFIG_INPUT"}, parameters[0].EnvVarMapping)
			require.Nil(t, parameters[0].Value)
		})
	}
}

func TestManagerRejectsReservedVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		aliases map[string]string
		wantErr string
	}{
		{
			name: "provider deployment context",
			aliases: map[string]string{
				environment.SubscriptionIdEnvVarName: "PROJECT_SUBSCRIPTION",
			},
			wantErr: `provider output variable "AZURE_SUBSCRIPTION_ID" is reserved by azd`,
		},
		{
			name: "project deployment context",
			aliases: map[string]string{
				"PROVIDER_LOCATION": environment.LocationEnvVarName,
			},
			wantErr: `project output variable "AZURE_LOCATION" is reserved by azd`,
		},
		{
			name: "provider control variable",
			aliases: map[string]string{
				"azd_internal_control": "PROJECT_VALUE",
			},
			wantErr: `provider output variable "azd_internal_control" is reserved by azd`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, nil, environment.New("test"),
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)

			err := manager.Initialize(t.Context(), "", provisioning.Options{
				Provider: provisioning.Bicep,
				Outputs:  tt.aliases,
			})

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestManagerVirtualInputsUseProviderView(t *testing.T) {
	t.Parallel()

	provider := &variableTestProvider{}
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
		return provider
	})
	manager := provisioning.NewManager(
		mockContext.Container, defaultProvider, nil, environment.New("test"),
		mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	virtualEnv := map[string]string{
		"FIRST": "first", "SECOND": "second", "THIRD": "third", "UNCHANGED": "other",
	}
	require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
		Provider:   provisioning.Bicep,
		VirtualEnv: virtualEnv,
		Env: osutil.ExpandableMap{
			"FIRST":     osutil.NewExpandableString("${SECOND}"),
			"SECOND":    osutil.NewExpandableString("${FIRST}"),
			"LOCAL":     osutil.NewExpandableString("${THIRD}"),
			"UNCHANGED": osutil.NewExpandableString("${MISSING}"),
		},
	}))

	require.Equal(t, map[string]string{
		"FIRST": "", "SECOND": "", "THIRD": "third", "LOCAL": "",
	},
		provider.options.VirtualEnv)
	require.Equal(t, map[string]string{
		"FIRST": "first", "SECOND": "second", "THIRD": "third", "UNCHANGED": "other",
	}, virtualEnv)
}

func TestManagerDeployValidatesOutputMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		aliases map[string]string
		want    map[string]string
		wantErr string
	}{
		{
			name: "collision with unmapped output", aliases: map[string]string{"FIRST": "SECOND"},
			wantErr: `provider outputs "FIRST" and "SECOND" both target project variable "SECOND"`,
		},
		{
			name: "overlapping output mappings applied once", aliases: map[string]string{"FIRST": "SECOND", "SECOND": "SHARED"},
			want: map[string]string{"FIRST": "old-first", "SECOND": "one", "SHARED": "two"},
		},
		{
			name: "identity output mapping", aliases: map[string]string{"FIRST": "FIRST"},
			want: map[string]string{"FIRST": "one", "SECOND": "two", "SHARED": "old-shared"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := map[string]string{"FIRST": "old-first", "SECOND": "old-second", "SHARED": "old-shared"}
			backing := environment.NewWithValues("test", original)
			before := backing.Dotenv()
			provider := &variableTestProvider{deployment: &provisioning.Deployment{
				Outputs: map[string]provisioning.OutputParameter{
					"FIRST":  {Type: provisioning.ParameterTypeString, Value: "one"},
					"SECOND": {Type: provisioning.ParameterTypeString, Value: "two"},
				},
			}}
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Bicep), func() provisioning.Provider {
				return provider
			})
			envManager := &mockenv.MockEnvManager{}
			if tt.wantErr == "" {
				envManager.On("Save", mock.Anything, backing).Return(nil).Once()
			}
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, envManager, backing,
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)
			require.NoError(t, manager.Initialize(t.Context(), "", provisioning.Options{
				Provider: provisioning.Bicep, Outputs: tt.aliases,
			}))

			result, err := manager.Deploy(t.Context())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, result)
				require.Equal(t, before, backing.Dotenv(), "no output should be written on collision")
				envManager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				require.NoError(t, err)
				require.Same(t, provider.deployment, result.Deployment)
				require.Equal(t, tt.want, backing.Dotenv())
			}
			require.Contains(t, provider.deployment.Outputs, "FIRST")
			require.NotContains(t, provider.deployment.Outputs, "SHARED")
			envManager.AssertExpectations(t)
		})
	}
}

type savingEnvironmentProvider struct {
	provisioning.Provider
	env        environment.ScopedEnvironment
	envManager environment.Manager
}

func (p *savingEnvironmentProvider) Initialize(ctx context.Context, _ string, _ provisioning.Options) error {
	if err := p.env.GetConfig().Set("infra.parameters.input", p.env.Getenv("LOCAL_INPUT")); err != nil {
		return err
	}
	p.env.DotenvSet("PROVIDER_WRITE", "saved")
	if err := p.envManager.Save(ctx, p.env.BackingEnv()); err != nil {
		return err
	}

	return p.envManager.SaveWithOptions(ctx, p.env.BackingEnv(), nil)
}

func (p *savingEnvironmentProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	return (&mappedEnvironmentProvider{}).Deploy(ctx)
}

// BACKCOMPAT: upstream/main lets providers (Bicep in particular) use the live shared environment, its config and
// its env manager directly, while manager outputs are staged in the layer's own environment. If this test breaks,
// providers have lost that direct access: confirm the change is intentional before editing the test.
func TestCompat_ProviderKeepsDirectEnvironmentAccess(t *testing.T) {
	t.Parallel()

	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			sharedEnv := environment.NewWithValues("shared", map[string]string{
				"LOCAL_INPUT": "live-input", "SHARED_INPUT": "live-input",
			})
			mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
				return sharedEnv
			})
			parentManager := &mockenv.MockEnvManager{}
			parentManager.On("Save", mock.Anything, sharedEnv).Return(nil).Once()
			parentManager.On("SaveWithOptions", mock.Anything, sharedEnv, (*environment.SaveOptions)(nil)).
				Return(nil).Once()
			mockContext.Container.MustRegisterSingleton(func() environment.Manager {
				return parentManager
			})

			layerEnv := environment.NewWithValues("shared", map[string]string{
				"LOCAL_INPUT": "snapshot-input", "SHARED_INPUT": "snapshot-input",
			})
			layerManager := &mockenv.MockEnvManager{}
			layerManager.On("Save", mock.Anything, layerEnv).Return(nil).Once()
			var provider *savingEnvironmentProvider
			mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
				func(env environment.ScopedEnvironment, envManager environment.Manager) provisioning.Provider {
					provider = &savingEnvironmentProvider{env: env, envManager: envManager}
					return provider
				})

			options := provisioning.Options{Provider: provisioning.Bicep}
			if configured {
				options.Env = osutil.ExpandableMap{"LOCAL_INPUT": osutil.NewExpandableString("${SHARED_INPUT}")}
				options.Outputs = map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"}
			}
			manager := provisioning.NewManager(
				mockContext.Container, defaultProvider, layerManager, layerEnv,
				mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
			)

			require.NoError(t, manager.Initialize(t.Context(), "", options))
			require.Same(t, sharedEnv, provider.env.BackingEnv())
			require.Same(t, parentManager, provider.envManager)
			value, has := sharedEnv.Config.GetString("infra.parameters.input")
			require.True(t, has)
			require.Equal(t, "live-input", value)
			require.Equal(t, "saved", sharedEnv.Getenv("PROVIDER_WRITE"))
			require.Empty(t, layerEnv.Getenv("PROVIDER_WRITE"))
			require.True(t, layerEnv.Config.IsEmpty())

			_, err := manager.Deploy(t.Context())
			require.NoError(t, err)
			outputKey := "LOCAL_OUTPUT"
			if configured {
				outputKey = "SHARED_OUTPUT"
				require.Empty(t, layerEnv.Getenv("LOCAL_OUTPUT"))
			}
			require.Equal(t, "output-value", layerEnv.Getenv(outputKey))
			require.Empty(t, sharedEnv.Getenv(outputKey), "manager outputs must remain staged in the clone")
			layerManager.AssertExpectations(t)
			parentManager.AssertExpectations(t)
			var resolvedManager environment.Manager
			require.NoError(t, mockContext.Container.Resolve(&resolvedManager))
			require.Same(t, parentManager, resolvedManager)
		})
	}
}

func (p *mappedEnvironmentProvider) Initialize(context.Context, string, provisioning.Options) error {
	p.initializedInput = p.env.Getenv("LOCAL_INPUT")
	return nil
}

func (p *mappedEnvironmentProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	return &provisioning.DeployResult{
		Deployment: &provisioning.Deployment{
			Outputs: map[string]provisioning.OutputParameter{
				"LOCAL_OUTPUT": {
					Type:  provisioning.ParameterTypeString,
					Value: "output-value",
				},
			},
		},
	}, nil
}

func (p *mappedEnvironmentProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return &provisioning.DestroyResult{
		InvalidatedEnvKeys: []string{"LOCAL_OUTPUT"},
	}, nil
}

func TestProviderInputsAndOutputMappingsAreIndependent(t *testing.T) {
	backing := environment.NewWithValues("test-env", map[string]string{
		"SHARED_INPUT": "input-value",
		"LOCAL_OUTPUT": "unrelated-value",
		"OUTPUT_INPUT": "input-side",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil).Twice()

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
		return backing
	})
	var provider *mappedEnvironmentProvider
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &mappedEnvironmentProvider{env: env}
			return provider
		})

	manager := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		backing,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	options := provisioning.Options{
		Provider: provisioning.Bicep,
		Env: osutil.ExpandableMap{
			"LOCAL_INPUT":  osutil.NewExpandableString("${SHARED_INPUT}"),
			"LOCAL_OUTPUT": osutil.NewExpandableString("${OUTPUT_INPUT}"),
		},
		Outputs: map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"},
	}

	require.NoError(t, manager.Initialize(t.Context(), "", options))
	require.Equal(t, "input-value", provider.initializedInput,
		"the provider should read LOCAL_INPUT from SHARED_INPUT through its scoped environment")

	_, err := manager.Deploy(t.Context())
	require.NoError(t, err)
	require.Equal(t, "output-value", backing.Getenv("SHARED_OUTPUT"),
		"the provider's LOCAL_OUTPUT should be persisted as SHARED_OUTPUT")
	require.Equal(t, "unrelated-value", backing.Getenv("LOCAL_OUTPUT"),
		"publishing the output should not overwrite the backing LOCAL_OUTPUT")
	require.Equal(t, "input-side", provider.env.Getenv("LOCAL_OUTPUT"),
		"an output mapping must not change the provider's input value")

	_, err = manager.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))
	require.NoError(t, err)
	require.Empty(t, backing.Getenv("SHARED_OUTPUT"),
		"invalidating the provider's LOCAL_OUTPUT should delete SHARED_OUTPUT")
	require.Equal(t, "unrelated-value", backing.Getenv("LOCAL_OUTPUT"),
		"destroying the output should not delete the backing LOCAL_OUTPUT")
	require.Equal(t, "input-side", provider.env.Getenv("LOCAL_OUTPUT"))
	require.Equal(t, "input-side", backing.Getenv("OUTPUT_INPUT"))
	envManager.AssertExpectations(t)
}

func (p *destroyResultProvider) Initialize(context.Context, string, provisioning.Options) error {
	return nil
}

func (p *destroyResultProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return p.result, nil
}

func TestManagerDestroy_InvalidatesMappedOutput(t *testing.T) {
	t.Parallel()

	const (
		logicalKey  = "OUTPUT"
		physicalKey = "LAYER_OUTPUT"
	)

	backing := environment.NewWithValues("test-env", map[string]string{
		logicalKey:  "shared-value",
		physicalKey: "layer-value",
	})
	provider := &destroyResultProvider{
		result: &provisioning.DestroyResult{InvalidatedEnvKeys: []string{logicalKey}},
	}

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedSingleton(string(provisioning.Test), func() provisioning.Provider {
		return provider
	})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil)

	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		backing,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	require.NoError(t, mgr.Initialize(t.Context(), "", provisioning.Options{
		Provider: provisioning.Test, Outputs: map[string]string{logicalKey: physicalKey},
	}))

	_, err := mgr.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))

	require.NoError(t, err)
	require.Equal(t, "shared-value", backing.Getenv(logicalKey))
	require.Empty(t, backing.Getenv(physicalKey))
	envManager.AssertExpectations(t)
}

func registerContainerDependencies(
	mockContext *mocks.MockContext, env *environment.Environment,
) *mockenv.MockEnvManager {
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	mockContext.Container.MustRegisterSingleton(func() environment.Manager {
		return envManager
	})

	mockContext.Container.MustRegisterSingleton(func() account.SubscriptionCredentialProvider {
		return mockContext.SubscriptionCredentialProvider
	})
	mockContext.Container.MustRegisterSingleton(func() *policy.ClientOptions {
		return mockContext.ArmClientOptions
	})

	mockContext.Container.MustRegisterSingleton(azapi.NewResourceService)
	mockContext.Container.MustRegisterSingleton(func() config.UserConfigManager {
		return config.NewUserConfigManager(mockContext.ConfigManager)
	})
	mockContext.Container.MustRegisterSingleton(prompt.NewDefaultPrompter)
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Test), test.NewTestProvider)
	mockContext.Container.MustRegisterSingleton(func() account.Manager {
		return &mockaccount.MockAccountManager{
			Subscriptions: []account.Subscription{
				{
					Id:   "00000000-0000-0000-0000-000000000000",
					Name: "test",
				},
			},
			Locations: []account.Location{
				{
					Name:                "location",
					DisplayName:         "Test Location",
					RegionalDisplayName: "(US) Test Location",
				},
			},
		}
	})
	mockContext.Container.MustRegisterSingleton(func() *environment.Environment {
		return env
	})
	mockContext.Container.MustRegisterSingleton(func() environment.ScopedEnvironment {
		return env
	})
	mockContext.Container.MustRegisterSingleton(func() *azapi.AzureClient {
		return mockazapi.NewAzureClientFromMockContext(mockContext)
	})

	mockContext.Container.MustRegisterSingleton(func() clock.Clock {
		return clock.NewMock()
	})

	mockContext.Container.MustRegisterSingleton(func() *cloud.Cloud {
		return cloud.AzurePublic()
	})

	return envManager
}

func defaultProvider() (provisioning.ProviderKind, error) {
	return provisioning.Bicep, nil
}

func TestRecordInfraProviderUsage(t *testing.T) {
	failingResolver := func() (provisioning.ProviderKind, error) {
		return provisioning.NotSpecified, errors.New("no default provider")
	}
	unspecifiedResolver := func() (provisioning.ProviderKind, error) {
		return provisioning.NotSpecified, nil
	}

	tests := []struct {
		name            string
		layers          []provisioning.Options
		defaultProvider provisioning.DefaultProviderResolver
		expected        []string // nil means no infra.provider attribute is recorded
	}{
		{
			name:            "single explicit bicep",
			layers:          []provisioning.Options{{Provider: provisioning.Bicep}},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name:            "single explicit terraform",
			layers:          []provisioning.Options{{Provider: provisioning.Terraform}},
			defaultProvider: defaultProvider,
			expected:        []string{"terraform"},
		},
		{
			name:            "unspecified resolves through default",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name: "uniform provider across layers",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.Bicep},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep"},
		},
		{
			name: "different providers across layers record each provider sorted",
			// Input is intentionally in reverse-sorted order (terraform before bicep) so the case
			// verifies the sorting contract, not just de-duplication.
			layers: []provisioning.Options{
				{Provider: provisioning.Terraform},
				{Provider: provisioning.Bicep},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep", "terraform"},
		},
		{
			name:            "single explicit arm",
			layers:          []provisioning.Options{{Provider: provisioning.Arm}},
			defaultProvider: defaultProvider,
			expected:        []string{"arm"},
		},
		{
			name:            "single explicit pulumi",
			layers:          []provisioning.Options{{Provider: provisioning.Pulumi}},
			defaultProvider: defaultProvider,
			expected:        []string{"pulumi"},
		},
		{
			name:            "custom provider is bucketed",
			layers:          []provisioning.Options{{Provider: provisioning.ProviderKind("my-extension-provider")}},
			defaultProvider: defaultProvider,
			expected:        []string{provisioning.InfraProviderCustom},
		},
		{
			name: "built-in plus custom records both",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.ProviderKind("my-extension-provider")},
			},
			defaultProvider: defaultProvider,
			expected:        []string{"bicep", provisioning.InfraProviderCustom},
		},
		{
			name: "two distinct custom providers collapse to custom",
			layers: []provisioning.Options{
				{Provider: provisioning.ProviderKind("vendor.one")},
				{Provider: provisioning.ProviderKind("vendor.two")},
			},
			defaultProvider: defaultProvider,
			expected:        []string{provisioning.InfraProviderCustom},
		},
		{
			name:            "no layers records nothing",
			layers:          nil,
			defaultProvider: defaultProvider,
			expected:        nil,
		},
		{
			name:            "unspecified with nil resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: nil,
			expected:        nil,
		},
		{
			name:            "unspecified with failing resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: failingResolver,
			expected:        nil,
		},
		{
			name:            "unspecified resolving to NotSpecified records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: unspecifiedResolver,
			expected:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Record onto a real span captured by an in-memory recorder so the test verifies the
			// attribute lands directly on the command span (not the process-global usage bag).
			sr := tracetest.NewSpanRecorder()
			tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
			ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.test")

			// RecordInfraProviderUsage only reads the manager's default provider, so the remaining
			// dependencies are intentionally nil.
			mgr := provisioning.NewManager(nil, tt.defaultProvider, nil, nil, nil, nil, nil, nil)
			mgr.RecordInfraProviderUsage(ctx, tt.layers)
			span.End()

			ended := sr.Ended()
			require.Len(t, ended, 1)

			var got []string
			var found bool
			for _, attr := range ended[0].Attributes() {
				if attr.Key == fields.InfraProviderKey.Key {
					got = attr.Value.AsStringSlice()
					found = true
				}
			}

			if tt.expected == nil {
				require.False(t, found, "expected no infra.provider attribute, got %v", got)
				return
			}

			require.True(t, found, "expected infra.provider attribute to be recorded")
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestRecordInfraProviderUsage_ResolvesDefaultOnce verifies the "default provider at most once per
// call" contract: multiple unspecified layers must all resolve through the manager's default
// provider while invoking that (potentially I/O-bound) resolver exactly once, and collapse to the
// single resolved value.
func TestRecordInfraProviderUsage_ResolvesDefaultOnce(t *testing.T) {
	var calls atomic.Int32
	countingResolver := func() (provisioning.ProviderKind, error) {
		calls.Add(1)
		return provisioning.Bicep, nil
	}

	sr := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.test")

	layers := []provisioning.Options{
		{Provider: provisioning.NotSpecified},
		{Provider: provisioning.NotSpecified},
		{Provider: provisioning.NotSpecified},
	}

	mgr := provisioning.NewManager(nil, countingResolver, nil, nil, nil, nil, nil, nil)
	mgr.RecordInfraProviderUsage(ctx, layers)
	span.End()

	require.Equal(t, int32(1), calls.Load(), "default provider resolver must be invoked at most once per call")

	ended := sr.Ended()
	require.Len(t, ended, 1)

	var got []string
	var found bool
	for _, attr := range ended[0].Attributes() {
		if attr.Key == fields.InfraProviderKey.Key {
			got = attr.Value.AsStringSlice()
			found = true
		}
	}

	require.True(t, found, "expected infra.provider attribute to be recorded")
	require.Equal(t, []string{"bicep"}, got)
}

// TestRecordInfraProviderUsage_DoesNotLeakToSiblingSpans is a regression test for the custom
// `workflows.up` leak: recording infra.provider must attach to the command's own span rather than
// the process-global usage bag. TelemetryMiddleware copies that bag onto every span it ends, so a
// bag-based value written by a nested `provision` step would leak onto a subsequent in-process
// `deploy` span (and could overwrite the parent up aggregate). This asserts the value lands on the
// provision span only, stays out of the global usage bag, and therefore does not reach a sibling
// deploy span even when that span is finished the way the middleware finishes it.
func TestRecordInfraProviderUsage_DoesNotLeakToSiblingSpans(t *testing.T) {
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)

	sr := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))

	mgr := provisioning.NewManager(nil, func() (provisioning.ProviderKind, error) {
		return provisioning.Bicep, nil
	}, nil, nil, nil, nil, nil, nil)

	// Nested `provision` step records onto its own span.
	provisionCtx, provisionSpan := tp.Tracer("test").Start(t.Context(), "cmd.provision")
	mgr.RecordInfraProviderUsage(provisionCtx, []provisioning.Options{{Provider: provisioning.Bicep}})
	provisionSpan.End()

	// Subsequent in-process `deploy` step: the telemetry middleware finishes its span by copying
	// the process-global usage bag onto it. With span-scoped recording the bag is empty of
	// infra.provider, so nothing leaks.
	_, deploySpan := tp.Tracer("test").Start(t.Context(), "cmd.deploy")
	deploySpan.SetAttributes(tracing.GetUsageAttributes()...)
	deploySpan.End()

	byName := map[string][]attribute.KeyValue{}
	for _, s := range sr.Ended() {
		byName[s.Name()] = s.Attributes()
	}

	hasInfraProvider := func(attrs []attribute.KeyValue) bool {
		for _, a := range attrs {
			if a.Key == fields.InfraProviderKey.Key {
				return true
			}
		}
		return false
	}

	require.True(t, hasInfraProvider(byName["cmd.provision"]), "provision span should carry infra.provider")
	require.False(t, hasInfraProvider(byName["cmd.deploy"]),
		"infra.provider must not leak onto the sibling deploy span")
	require.False(t, hasInfraProvider(tracing.GetUsageAttributes()),
		"infra.provider must not be written to the process-global usage bag")
}
