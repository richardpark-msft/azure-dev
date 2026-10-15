// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type lifecycleDeployCall struct {
	subscriptionID string
	location       string
	name           string
	template       azure.RawArmTemplate
	parameters     azure.ArmParameters
	tags           map[string]*string
}

type lifecycleDeploymentService struct {
	azapi.DeploymentService
	deployments map[string]*azapi.ResourceDeployment
	deployCalls []lifecycleDeployCall
	deleteCalls []string
}

func newLifecycleDeploymentService() *lifecycleDeploymentService {
	return &lifecycleDeploymentService{
		deployments: map[string]*azapi.ResourceDeployment{},
	}
}

func (s *lifecycleDeploymentService) GenerateDeploymentName(baseName string) string {
	return baseName + "-deployment"
}

func (s *lifecycleDeploymentService) ValidatePreflightToSubscription(
	context.Context,
	string,
	string,
	string,
	azure.RawArmTemplate,
	azure.ArmParameters,
	map[string]*string,
	map[string]any,
) error {
	return nil
}

func (s *lifecycleDeploymentService) DeployToSubscription(
	_ context.Context,
	subscriptionID string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	_ map[string]any,
) (*azapi.ResourceDeployment, error) {
	layerValue, ok := parameters["layerValue"]
	if !ok {
		return nil, fmt.Errorf("layerValue parameter was not supplied")
	}

	outputValue := fmt.Sprintf("%v-output", layerValue.Value)
	deployment := &azapi.ResourceDeployment{
		Name:              deploymentName,
		Location:          location,
		Tags:              maps.Clone(tags),
		Outputs:           map[string]any{"OUTPUT": map[string]any{"type": "string", "value": outputValue}},
		Timestamp:         time.Now(),
		ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
	}
	s.deployments[deploymentName] = deployment
	s.deployCalls = append(s.deployCalls, lifecycleDeployCall{
		subscriptionID: subscriptionID,
		location:       location,
		name:           deploymentName,
		template:       slices.Clone(armTemplate),
		parameters:     maps.Clone(parameters),
		tags:           maps.Clone(tags),
	})

	return deployment, nil
}

func (s *lifecycleDeploymentService) ListSubscriptionDeployments(
	context.Context,
	string,
) ([]*azapi.ResourceDeployment, error) {
	return slices.Collect(maps.Values(s.deployments)), nil
}

func (s *lifecycleDeploymentService) ListSubscriptionDeploymentResources(
	context.Context,
	string,
	string,
) ([]*armresources.ResourceReference, error) {
	return nil, nil
}

func (s *lifecycleDeploymentService) DeleteSubscriptionDeployment(
	_ context.Context,
	_ string,
	deploymentName string,
	_ map[string]any,
	_ *async.Progress[azapi.DeleteDeploymentProgress],
) error {
	if _, ok := s.deployments[deploymentName]; !ok {
		return fmt.Errorf("deployment %q was not created", deploymentName)
	}

	delete(s.deployments, deploymentName)
	s.deleteCalls = append(s.deleteCalls, deploymentName)
	return nil
}

func TestBicepLifecycle_MapsLayerInputsOutputsAndTeardown(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(configDir, "bin", "bicep"))
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("AZD_FORCE_TTY", "false")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MISSING_INPUT", "")

	projectDir := t.TempDir()
	infraDir := filepath.Join(projectDir, "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(`
name: lifecycle-test
infra:
  layers:
    - name: app
      provider: bicep
      path: infra
      module: main
      env:
        LAYER_VALUE: prefix-${APP_INPUT}-${MISSING_INPUT:-fallback}
      outputs:
        OUTPUT: APP_OUTPUT
    - name: data
      provider: bicep
      path: infra
      module: main
      env:
        LAYER_VALUE: ${DATA_INPUT}
      outputs:
        OUTPUT: DATA_OUTPUT
`), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(infraDir, "main.bicep"),
		[]byte("targetScope = 'subscription'\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.parameters.json"), []byte(
		`{"parameters":{"layerValue":{"value":"${LAYER_VALUE}"}}}`), 0o600))

	projectConfig, err := project.Load(t.Context(), filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	layers := projectConfig.Infra.GetLayers()
	require.Len(t, layers, 2)
	expectedInputs := map[string]string{"app": "prefix-app-fallback", "data": "data"}

	backing := environment.NewWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "test-subscription",
		environment.LocationEnvVarName:       "westus2",
		"OUTPUT":                             "shared-output",
		"APP_INPUT":                          "app",
		"DATA_INPUT":                         "data",
		"APP_OUTPUT":                         "stale-app-output",
		"DATA_OUTPUT":                        "stale-data-output",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil)
	deployments := newLifecycleDeploymentService()

	type layerLifecycle struct {
		options     provisioning.Options
		physicalKey string
		manager     *provisioning.Manager
	}

	layerLifecycles := make([]layerLifecycle, 0, len(layers))
	for _, layer := range layers {
		outputKey := strings.ToUpper(layer.Name) + "_OUTPUT"
		layer.IgnoreDeploymentState = true

		manager := newLifecycleManager(t, projectDir, layer, backing, envManager, deployments)
		result, err := manager.Deploy(t.Context())
		require.NoError(t, err)
		require.Equal(t, expectedInputs[layer.Name]+"-output", result.Deployment.Outputs["OUTPUT"].Value)
		require.NotContains(t, result.Deployment.Outputs, outputKey)

		layerLifecycles = append(layerLifecycles, layerLifecycle{
			options:     layer,
			physicalKey: outputKey,
			manager:     manager,
		})
	}

	require.Equal(t, "shared-output", backing.Getenv("OUTPUT"))
	require.Equal(t, "prefix-app-fallback-output", backing.Getenv("APP_OUTPUT"))
	require.Equal(t, "data-output", backing.Getenv("DATA_OUTPUT"))
	require.NotContains(t, backing.Dotenv(), "LAYER_VALUE")
	require.Equal(t, "app", backing.Getenv("APP_INPUT"))
	require.Equal(t, "data", backing.Getenv("DATA_INPUT"))
	require.Len(t, deployments.deployCalls, 2)

	for index, layer := range layers {
		call := deployments.deployCalls[index]
		require.Equal(t, "test-subscription", call.subscriptionID)
		require.Equal(t, "westus2", call.location)
		require.Equal(t, fmt.Sprintf("test-env-%s-deployment", layer.Name), call.name)
		require.Contains(t, string(call.template), `"type":"Microsoft.Resources/resourceGroups"`)
		require.Equal(t, expectedInputs[layer.Name], call.parameters["layerValue"].Value)
		require.Equal(t, layer.Name, *call.tags[azure.TagKeyAzdLayerName])
		require.Equal(t, "lifecycle-test", *call.tags[azure.TagKeyAzdProjectName])
	}

	slices.Reverse(layerLifecycles)
	for index, layer := range layerLifecycles {
		layer.options.Mode = provisioning.ModeDestroy
		require.NoError(t, layer.manager.Initialize(t.Context(), projectDir, layer.options))

		destroyResult, err := layer.manager.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))
		require.NoError(t, err)
		require.Equal(t, []string{"OUTPUT"}, destroyResult.InvalidatedEnvKeys)
		require.Empty(t, backing.Getenv(layer.physicalKey))
		require.Equal(t, "shared-output", backing.Getenv("OUTPUT"))

		if index == 0 {
			require.Equal(t, "prefix-app-fallback-output", backing.Getenv("APP_OUTPUT"))
		}
	}

	require.Equal(t, []string{"test-env-data-deployment", "test-env-app-deployment"}, deployments.deleteCalls)
	require.Empty(t, deployments.deployments)
	envManager.AssertExpectations(t)
}

type outputOnlyLifecycleProvider struct {
	provisioning.Provider
	env       environment.ScopedEnvironment
	inputs    map[string]string
	outputs   map[string]provisioning.OutputParameter
	destroyed []string
}

func (p *outputOnlyLifecycleProvider) Initialize(context.Context, string, provisioning.Options) error {
	p.inputs = map[string]string{
		"LOCAL_OUTPUT":  p.env.Getenv("LOCAL_OUTPUT"),
		"SHARED_OUTPUT": p.env.Getenv("SHARED_OUTPUT"),
	}
	return nil
}

func (p *outputOnlyLifecycleProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	return &provisioning.DeployResult{Deployment: &provisioning.Deployment{Outputs: p.outputs}}, nil
}

func (p *outputOnlyLifecycleProvider) State(
	context.Context, *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return &provisioning.StateResult{State: &provisioning.State{Outputs: p.outputs}}, nil
}

func (p *outputOnlyLifecycleProvider) PlannedOutputs(context.Context) ([]provisioning.PlannedOutput, error) {
	return []provisioning.PlannedOutput{{Name: "LOCAL_OUTPUT"}, {Name: "UNMAPPED"}}, nil
}

func (p *outputOnlyLifecycleProvider) Destroy(
	context.Context, provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return &provisioning.DestroyResult{InvalidatedEnvKeys: p.destroyed}, nil
}

func TestCustomProviderLifecycle_OutputMappingsDoNotOverlayInputs(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	backing := environment.NewWithValues("test-env", map[string]string{
		"LOCAL_OUTPUT": "local-input", "SHARED_OUTPUT": "shared-input", "UNTOUCHED": "retained",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil)
	ioc.RegisterInstance[environment.ScopedEnvironment](mockContext.Container, backing)
	var provider *outputOnlyLifecycleProvider
	mockContext.Container.MustRegisterNamedTransient("custom",
		func(env environment.ScopedEnvironment) provisioning.Provider {
			provider = &outputOnlyLifecycleProvider{
				env: env,
				outputs: map[string]provisioning.OutputParameter{
					"LOCAL_OUTPUT": {Type: provisioning.ParameterTypeString, Value: "produced"},
					"UNMAPPED":     {Type: provisioning.ParameterTypeString, Value: "unmapped"},
				},
				destroyed: []string{"LOCAL_OUTPUT", "UNMAPPED"},
			}
			return provider
		})
	manager := provisioning.NewManager(
		mockContext.Container, func() (provisioning.ProviderKind, error) { return "custom", nil },
		envManager, backing, mockContext.Console, mockContext.AlphaFeaturesManager, nil, cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), t.TempDir(), provisioning.Options{
		Provider: "custom", Outputs: map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"},
	}))
	require.Same(t, backing, provider.env, "output-only mappings must not create an input overlay")
	require.Equal(t, map[string]string{
		"LOCAL_OUTPUT": "local-input", "SHARED_OUTPUT": "shared-input",
	}, provider.inputs)

	result, err := manager.Deploy(t.Context())
	require.NoError(t, err)
	expected := map[string]provisioning.OutputParameter{
		"SHARED_OUTPUT": provider.outputs["LOCAL_OUTPUT"],
		"UNMAPPED":      provider.outputs["UNMAPPED"],
	}
	require.Equal(t, provider.outputs, result.Deployment.Outputs)
	require.Equal(t, "local-input", backing.Getenv("LOCAL_OUTPUT"))
	require.Equal(t, "produced", backing.Getenv("SHARED_OUTPUT"))
	require.Equal(t, "unmapped", backing.Getenv("UNMAPPED"))

	state, err := manager.State(t.Context(), &provisioning.StateOptions{})
	require.NoError(t, err)
	require.Equal(t, expected, state.State.Outputs)
	planned, err := manager.PlannedOutputs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []provisioning.PlannedOutput{{Name: "SHARED_OUTPUT"}, {Name: "UNMAPPED"}}, planned)
	require.Contains(t, provider.outputs, "LOCAL_OUTPUT")
	require.NotContains(t, provider.outputs, "SHARED_OUTPUT")

	destroyed, err := manager.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))
	require.NoError(t, err)
	require.Equal(t, []string{"LOCAL_OUTPUT", "UNMAPPED"}, destroyed.InvalidatedEnvKeys)
	require.Equal(t, []string{"LOCAL_OUTPUT", "UNMAPPED"}, provider.destroyed)
	require.NotContains(t, backing.Dotenv(), "SHARED_OUTPUT")
	require.NotContains(t, backing.Dotenv(), "UNMAPPED")
	require.Equal(t, "local-input", backing.Getenv("LOCAL_OUTPUT"))
	require.Equal(t, "retained", backing.Getenv("UNTOUCHED"))
	envManager.AssertExpectations(t)
}

func newLifecycleManager(
	t *testing.T,
	projectDir string,
	options provisioning.Options,
	env environment.ScopedEnvironment,
	envManager environment.Manager,
	deployments azapi.DeploymentService,
) *provisioning.Manager {
	t.Helper()

	// MockContext panics on every unmatched HTTP request. This test registers no HTTP responses,
	// so any attempt to bypass the fake deployment service and contact Azure fails immediately.
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)
	armTemplate := map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"parameters": map[string]any{
			"layerValue": map[string]any{"type": "string"},
		},
		"resources": []map[string]any{
			{
				"type":       "Microsoft.Resources/resourceGroups",
				"apiVersion": "2021-04-01",
				"name":       "rg-lifecycle-test",
				"location":   "[deployment().location]",
			},
		},
		"outputs": map[string]any{
			"OUTPUT": map[string]any{
				"type":  "string",
				"value": "[parameters('layerValue')]",
			},
		},
	}
	templateBytes, err := json.Marshal(armTemplate)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) > 0 && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) > 0 && args.Args[0] == "build"
	}).Respond(exec.RunResult{Stdout: string(templateBytes)})
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) > 0 && args.Args[0] == "snapshot"
	}).SetError(errors.New("snapshot is intentionally unavailable in this offline test"))

	resourceService := azapi.NewResourceService(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
	)
	deploymentManager := infra.NewDeploymentManager(deployments, &mockResourceManager{}, mockContext.Console)
	ioc.RegisterInstance[environment.ScopedEnvironment](mockContext.Container, env)
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep),
		func(providerEnv environment.ScopedEnvironment) provisioning.Provider {
			return NewBicepProvider(
				mockazapi.NewAzureClientFromMockContext(mockContext),
				bicep.NewCli(mockContext.Console, mockContext.CommandRunner),
				resourceService,
				&mockResourceManager{},
				deploymentManager,
				envManager,
				providerEnv,
				mockContext.Console,
				nil,
				&mockCurrentPrincipal{},
				nil,
				cloud.AzurePublic(),
				nil,
				nil,
				mockContext.Container,
			)
		})

	manager := provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) {
			return provisioning.Bicep, nil
		},
		envManager,
		env.BackingEnv(),
		mockContext.Console,
		alpha.NewFeaturesManagerWithConfig(mockContext.Config),
		nil,
		cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), projectDir, options))
	return manager
}
