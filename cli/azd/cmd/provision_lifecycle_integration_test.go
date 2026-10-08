// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal"
	internalcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infrabicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	biceptool "github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type lifecycleNetworkGuard struct {
	t *testing.T
}

func (guard lifecycleNetworkGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	err := fmt.Errorf("unexpected network request in offline lifecycle: %s %s", req.Method, req.URL.Host)
	guard.t.Error(err)
	return nil, err
}

type lifecycleDeployCall struct {
	name       string
	parameters azure.ArmParameters
	tags       map[string]*string
}

type lifecycleDeploymentService struct {
	azapi.DeploymentService
	mu           sync.Mutex
	deployments  map[string]*azapi.ResourceDeployment
	deployCalls  []lifecycleDeployCall
	deleteCalls  []string
	previewCalls int
}

func (s *lifecycleDeploymentService) GenerateDeploymentName(baseName string) string {
	return baseName + "-deployment"
}

func (s *lifecycleDeploymentService) CalculateTemplateHash(
	context.Context, string, azure.RawArmTemplate,
) (string, error) {
	return "offline-template-hash", nil
}

func (s *lifecycleDeploymentService) ValidatePreflightToSubscription(
	context.Context, string, string, string, azure.RawArmTemplate, azure.ArmParameters, map[string]*string, map[string]any,
) error {
	return nil
}

func (s *lifecycleDeploymentService) WhatIfDeployToSubscription(
	context.Context, string, string, string, azure.RawArmTemplate, azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previewCalls++
	return &armresources.WhatIfOperationResult{
		Status: new("Succeeded"), Properties: &armresources.WhatIfOperationProperties{},
	}, nil
}

func (s *lifecycleDeploymentService) DeployToSubscription(
	_ context.Context,
	subscriptionID string,
	location string,
	name string,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	_ map[string]any,
) (*azapi.ResourceDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subscriptionID != "00000000-0000-0000-0000-000000000000" || location != "westus2" {
		return nil, errors.New("deployment did not receive the shared subscription and location")
	}
	if !strings.Contains(string(template), `"layerValue"`) {
		return nil, errors.New("deployment did not receive the compiled template")
	}
	value, has := parameters["layerValue"]
	if !has {
		return nil, errors.New("deployment did not receive layerValue")
	}
	deployment := &azapi.ResourceDeployment{
		Name: name, Location: location, Tags: maps.Clone(tags),
		Outputs: map[string]any{
			"OUTPUT": map[string]any{"type": "string", "value": fmt.Sprintf("%v-output", value.Value)},
		},
		TemplateHash:      new("offline-template-hash"),
		Timestamp:         time.Now(),
		ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
	}
	s.deployments[name] = deployment
	s.deployCalls = append(s.deployCalls, lifecycleDeployCall{
		name: name, parameters: maps.Clone(parameters), tags: maps.Clone(tags),
	})
	return deployment, nil
}

func (s *lifecycleDeploymentService) ListSubscriptionDeployments(
	context.Context, string,
) ([]*azapi.ResourceDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.deployments)), nil
}

func (s *lifecycleDeploymentService) GetSubscriptionDeployment(
	_ context.Context, _ string, name string,
) (*azapi.ResourceDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deployment, has := s.deployments[name]
	if !has {
		return nil, fmt.Errorf("deployment %q was not created", name)
	}
	return deployment, nil
}

func (s *lifecycleDeploymentService) ListSubscriptionDeploymentResources(
	context.Context, string, string,
) ([]*armresources.ResourceReference, error) {
	return nil, nil
}

func (s *lifecycleDeploymentService) DeleteSubscriptionDeployment(
	_ context.Context, _ string, name string, _ map[string]any, _ *async.Progress[azapi.DeleteDeploymentProgress],
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, has := s.deployments[name]; !has {
		return fmt.Errorf("deployment %q was not created", name)
	}
	delete(s.deployments, name)
	s.deleteCalls = append(s.deleteCalls, name)
	return nil
}

type lifecycleResourceManager struct{}

func (*lifecycleResourceManager) WalkDeploymentOperations(
	context.Context, infra.Deployment, infra.WalkDeploymentOperationFunc,
) error {
	return nil
}

func (*lifecycleResourceManager) GetResourceTypeDisplayName(
	_ context.Context, _ string, _ string, resourceType azapi.AzureResourceType,
) (string, error) {
	return azapi.GetResourceTypeDisplayName(resourceType), nil
}

func (*lifecycleResourceManager) GetResourceGroupsForEnvironment(
	context.Context, string, string,
) ([]*azapi.Resource, error) {
	return nil, nil
}

func (*lifecycleResourceManager) FindResourceGroupForEnvironment(context.Context, string, string) (string, error) {
	return "offline-resource-group", nil
}

type lifecyclePrincipal struct{}

func (lifecyclePrincipal) CurrentPrincipalId(context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}

func (lifecyclePrincipal) CurrentPrincipalType(context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

type bicepLifecycle struct {
	t           *testing.T
	ctx         *mocks.MockContext
	project     *project.ProjectConfig
	env         *environment.Environment
	envManager  environment.Manager
	store       environment.LocalDataStore
	deployments *lifecycleDeploymentService
	parameters  map[string]any
}

func newBicepLifecycle(t *testing.T, format string, bicepParams bool) *bicepLifecycle {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(configDir, "bicep"))
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("AZD_FORCE_TTY", "false")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("LAYER_VALUE", "stale-process-value")
	originalTransport := http.DefaultTransport
	http.DefaultTransport = lifecycleNetworkGuard{t: t}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	root := t.TempDir()
	entries := []provisioning.Options{
		{Name: "app", Provider: provisioning.Bicep, Path: "infra/app",
			ParamAliases:  map[string]string{"LAYER_VALUE": "APP_INPUT"},
			OutputAliases: map[string]string{"OUTPUT": "APP_OUTPUT"}},
		{Name: "data", Provider: provisioning.Bicep, Path: "infra/data", DependsOn: []string{"app"},
			ParamAliases:  map[string]string{"LAYER_VALUE": "APP_OUTPUT"},
			OutputAliases: map[string]string{"OUTPUT": "DATA_OUTPUT"}},
	}
	projectConfig := &project.ProjectConfig{Name: "lifecycle-test"}
	if format == "v1" {
		projectConfig.Infra = provisioning.Options{Layers: entries}
	} else {
		projectConfig.Layers = project.LayerConfigs{
			{Name: "app", Infra: entries[:1]},
			{Name: "data", Infra: entries[1:]},
		}
	}
	projectPath := filepath.Join(root, "azure.yaml")
	require.NoError(t, project.Save(t.Context(), projectConfig, projectPath))
	loaded, err := project.Load(t.Context(), projectPath)
	require.NoError(t, err)
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Path)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte(
			"targetScope = 'subscription'\nparam layerValue string\noutput OUTPUT string = layerValue\n"), 0o600))
		if bicepParams {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicepparam"), []byte(
				"using './main.bicep'\nparam layerValue = readEnvironmentVariable('LAYER_VALUE')\n"), 0o600))
		} else {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.parameters.json"), []byte(
				`{"parameters":{"layerValue":{"value":"${LAYER_VALUE}"}}}`), 0o600))
		}
	}

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)
	azdCtx := azdcontext.NewAzdContextWithDirectory(root)
	store := environment.NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
	envManager, err := environment.NewManager(mockContext.Container, azdCtx, mockContext.Console, store, nil)
	require.NoError(t, err)
	env := environment.NewWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "00000000-0000-0000-0000-000000000000",
		environment.LocationEnvVarName:       "westus2",
		"APP_INPUT":                          "app",
		"OUTPUT":                             "unrelated-output",
	})
	require.NoError(t, envManager.Save(t.Context(), env))
	lifecycle := &bicepLifecycle{
		t: t, ctx: mockContext, project: loaded, env: env, envManager: envManager, store: store,
		deployments: &lifecycleDeploymentService{deployments: map[string]*azapi.ResourceDeployment{}},
		parameters:  map[string]any{"layerValue": map[string]any{"type": "string"}},
	}
	// Both injected HTTP and subprocess mocks reject unmatched operations; no live Azure or tool fallback is allowed.
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == filepath.Join(configDir, "bicep") && len(args.Args) > 0 && args.Args[0] == "build"
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{Stdout: lifecycle.template()}, nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == filepath.Join(configDir, "bicep") && len(args.Args) > 0 && args.Args[0] == "build-params"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		values := map[string]string{}
		for _, value := range args.Env {
			key, value, has := strings.Cut(value, "=")
			if has {
				values[key] = value
			}
		}
		value, has := values["LAYER_VALUE"]
		if !has || value == "" || value == "stale-process-value" {
			return exec.RunResult{}, errors.New("compiler did not receive the layer-local input alias")
		}
		params, err := json.Marshal(azure.ArmParameterFile{
			Parameters: azure.ArmParameters{"layerValue": {Value: value}},
		})
		if err != nil {
			return exec.RunResult{}, err
		}
		result, err := json.Marshal(map[string]string{
			"templateJson": lifecycle.template(), "parametersJson": string(params),
		})
		return exec.RunResult{Stdout: string(result)}, err
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == filepath.Join(configDir, "bicep") && len(args.Args) > 0 && args.Args[0] == "snapshot"
	}).SetError(errors.New("snapshot unavailable in the offline fixture"))
	ioc.RegisterInstance(mockContext.Container, loaded)
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep), func(
		scopedEnv *environment.Environment, scopedManager environment.Manager,
	) provisioning.Provider {
		resourceService := azapi.NewResourceService(
			mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions,
		)
		resourceManager := &lifecycleResourceManager{}
		return infrabicep.NewBicepProvider(
			mockazapi.NewAzureClientFromMockContext(mockContext),
			biceptool.NewCli(mockContext.Console, mockContext.CommandRunner),
			resourceService, resourceManager,
			infra.NewDeploymentManager(lifecycle.deployments, resourceManager, mockContext.Console),
			scopedManager, scopedEnv, mockContext.Console,
			prompt.NewDefaultPrompter(scopedEnv, mockContext.Console, nil, nil, resourceService, cloud.AzurePublic()),
			lifecyclePrincipal{}, nil, cloud.AzurePublic(), nil, nil, mockContext.Container,
		)
	})
	return lifecycle
}

func (l *bicepLifecycle) template() string {
	l.t.Helper()
	raw, err := json.Marshal(map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     l.parameters,
		"resources": []map[string]any{{
			"type": "Microsoft.Resources/resourceGroups", "apiVersion": "2021-04-01",
			"name": "rg-offline-lifecycle", "location": "[deployment().location]",
		}},
		"outputs": map[string]any{
			"OUTPUT": map[string]any{"type": "string", "value": "[parameters('layerValue')]"},
		},
	})
	require.NoError(l.t, err)
	return string(raw)
}

func (l *bicepLifecycle) manager() *provisioning.Manager {
	return provisioning.NewManager(l.ctx.Container, nil, l.envManager, l.env, l.ctx.Console,
		l.ctx.AlphaFeaturesManager, nil, cloud.AzurePublic())
}

func (l *bicepLifecycle) provision(layer string, preview bool) error {
	l.t.Helper()
	require.NoError(l.t, l.envManager.Reload(l.t.Context(), l.env))
	cmd := internalcmd.NewProvisionCmd()
	flags := internalcmd.NewProvisionFlags(cmd, &internal.GlobalCommandOptions{})
	require.NoError(l.t, cmd.Flags().Set("preview", fmt.Sprint(preview)))
	pm := &mockProjectManager{}
	pm.On("InitializeServices", mock.Anything, mock.Anything).Return(nil)
	pm.On("EnsureAllTools", mock.Anything, mock.Anything).Return(nil)
	args := []string{}
	if layer != "" {
		args = append(args, layer)
	}
	resourceService := azapi.NewResourceService(l.ctx.SubscriptionCredentialProvider, l.ctx.ArmClientOptions)
	action := internalcmd.NewProvisionAction(
		args, flags, l.manager(), pm, project.NewImportManager(nil),
		project.NewResourceManager(l.env, nil, resourceService, &lifecycleResourceManager{}),
		l.project, l.env, l.envManager, l.ctx.Console, l.ctx.CommandRunner, l.ctx.Container,
		&output.NoneFormatter{}, io.Discard, nil, l.ctx.AlphaFeaturesManager, cloud.AzurePublic(), nil, nil,
	)
	_, err := action.Run(l.t.Context())
	pm.AssertExpectations(l.t)
	return err
}

func (l *bicepLifecycle) refresh() error {
	l.t.Helper()
	require.NoError(l.t, l.envManager.Reload(l.t.Context(), l.env))
	pm := &mockProjectManager{}
	pm.On("InitializeFrameworks", mock.Anything, mock.Anything).Return(nil, nil, nil)
	action := &envRefreshAction{
		flags: &envRefreshFlags{}, provisionManager: l.manager(), env: l.env, envManager: l.envManager,
		projectConfig: l.project, projectManager: pm, importManager: project.NewImportManager(nil),
		extensionActivator: &stubProviderActivator{}, console: l.ctx.Console,
		formatter: &output.NoneFormatter{}, writer: io.Discard, alphaFeatureManager: l.ctx.AlphaFeaturesManager,
	}
	_, err := action.Run(l.t.Context())
	pm.AssertExpectations(l.t)
	return err
}

func (l *bicepLifecycle) down(layer string) error {
	l.t.Helper()
	require.NoError(l.t, l.envManager.Reload(l.t.Context(), l.env))
	args := []string{}
	if layer != "" {
		args = append(args, layer)
	}
	action := &downAction{
		flags: &downFlags{forceDelete: true}, args: args,
		provisionManager: l.manager(), env: l.env, envManager: l.envManager, projectConfig: l.project,
		importManager: project.NewImportManager(nil), console: l.ctx.Console,
		alphaFeatureManager: l.ctx.AlphaFeaturesManager,
	}
	_, err := action.Run(l.t.Context())
	return err
}

func (l *bicepLifecycle) assertPersisted(expected map[string]string) {
	l.t.Helper()
	persisted, err := l.store.Get(l.t.Context(), l.env.Name())
	require.NoError(l.t, err)
	for key, value := range expected {
		require.Equal(l.t, value, persisted.Getenv(key), key)
	}
	require.Equal(l.t, "unrelated-output", persisted.Getenv("OUTPUT"))
	require.NotContains(l.t, persisted.Dotenv(), "LAYER_VALUE")
}

func TestBicepLifecycle_ProvisionRefreshDown(t *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		for _, bicepParams := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bicepparam=%t", format, bicepParams), func(t *testing.T) {
				lifecycle := newBicepLifecycle(t, format, bicepParams)
				require.NoError(t, lifecycle.provision("app", true))
				require.Equal(t, 1, lifecycle.deployments.previewCalls)
				require.Empty(t, lifecycle.deployments.deployCalls)

				require.NoError(t, lifecycle.provision("", false))
				require.Len(t, lifecycle.deployments.deployCalls, 2)
				require.Equal(t, "app", lifecycle.deployments.deployCalls[0].parameters["layerValue"].Value)
				require.Equal(t, "app-output", lifecycle.deployments.deployCalls[1].parameters["layerValue"].Value)
				lifecycle.assertPersisted(map[string]string{"APP_OUTPUT": "app-output", "DATA_OUTPUT": "app-output-output"})

				lifecycle.env.DotenvDelete("APP_OUTPUT")
				lifecycle.env.DotenvDelete("DATA_OUTPUT")
				require.NoError(t, lifecycle.envManager.Save(t.Context(), lifecycle.env))
				require.NoError(t, lifecycle.provision("", false))
				require.Len(t, lifecycle.deployments.deployCalls, 2, "unchanged deployments must reuse cached outputs")
				lifecycle.assertPersisted(map[string]string{"APP_OUTPUT": "app-output", "DATA_OUTPUT": "app-output-output"})

				lifecycle.env.DotenvSet("APP_OUTPUT", "stale")
				lifecycle.env.DotenvSet("DATA_OUTPUT", "stale")
				require.NoError(t, lifecycle.envManager.Save(t.Context(), lifecycle.env))
				require.NoError(t, lifecycle.refresh())
				lifecycle.assertPersisted(map[string]string{"APP_OUTPUT": "app-output", "DATA_OUTPUT": "app-output-output"})

				require.NoError(t, lifecycle.down("data"))
				lifecycle.assertPersisted(map[string]string{"APP_OUTPUT": "app-output", "DATA_OUTPUT": ""})
				require.NoError(t, lifecycle.down(""))
				lifecycle.assertPersisted(map[string]string{"APP_OUTPUT": "", "DATA_OUTPUT": ""})
				require.Equal(t, []string{"test-env-data-deployment", "test-env-app-deployment"},
					lifecycle.deployments.deleteCalls)
				require.Empty(t, lifecycle.deployments.deployments)
			})
		}
	}
}

func TestBicepLifecycle_SavedConfiguration(t *testing.T) {
	lifecycle := newBicepLifecycle(t, "v1", false)
	lifecycle.parameters["savedValue"] = map[string]any{"type": "string"}
	lifecycle.parameters["savedSecret"] = map[string]any{"type": "securestring"}
	require.NoError(t, lifecycle.env.Config.Set("infra.parameters.savedValue", "saved-value"))
	require.NoError(t, lifecycle.env.Config.SetSecret("infra.parameters.savedSecret", "saved-secret"))
	require.NoError(t, lifecycle.envManager.Save(t.Context(), lifecycle.env))

	require.NoError(t, lifecycle.provision("app", false))

	require.Len(t, lifecycle.deployments.deployCalls, 1)
	assert.Equal(t, "saved-value", lifecycle.deployments.deployCalls[0].parameters["savedValue"].Value)
	assert.Equal(t, "saved-secret", lifecycle.deployments.deployCalls[0].parameters["savedSecret"].Value)
}

func TestBicepLifecycle_PromptedConfigurationSurvivesReload(t *testing.T) {
	lifecycle := newBicepLifecycle(t, "v1", false)
	lifecycle.ctx.Console.SetNoPromptMode(false)
	lifecycle.parameters["promptedValue"] = map[string]any{"type": "string"}
	lifecycle.ctx.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "'promptedValue'")
	}).Respond("prompted-value")

	require.NoError(t, lifecycle.provision("app", true))

	persisted, err := lifecycle.store.Get(t.Context(), lifecycle.env.Name())
	require.NoError(t, err)
	value, has := persisted.Config.Get("infra.parameters.promptedValue")
	require.True(t, has, "provider prompt values must survive a real save/reload cycle")
	require.Equal(t, "prompted-value", value)
	lifecycle.ctx.Console.SetNoPromptMode(true)
	require.NoError(t, lifecycle.provision("app", false))
}
