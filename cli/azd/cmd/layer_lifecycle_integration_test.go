// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	internalcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lifecycleTestSubscription = "00000000-0000-0000-0000-000000000000"

const layerLifecycleProjectYAML = `name: layer-lifecycle-test
services:
  core-api:
    layer: core
    host: test
  core-worker:
    layer: core
    host: test
  database:
    layer: data
    host: test
  cache:
    layer: data
    host: test
  api:
    layer: app
    host: test
    uses:
      - database
  worker:
    layer: app
    host: test
  job:
    layer: jobs
    host: test
    dependsOn:
      - core
  scheduler:
    layer: jobs
    host: test

infra:
  layers:
    - name: core
      provider: bicep
      path: infra/core
    - name: data
      provider: bicep
      path: infra/data
      dependsOn:
        - core
`

const parallelLayerTreesProjectYAML = `name: parallel-layer-trees-test
infra: {layers: [
{name: core, provider: bicep, path: infra/core},
{name: data, provider: bicep, path: infra/data, dependsOn: [core]},
{name: monitoring, provider: bicep, path: infra/monitoring},
{name: alerts, provider: bicep, path: infra/alerts, dependsOn: [monitoring]}
]}
`

type layerLifecycleScenario struct {
	t *testing.T

	// we use a nested container int he a
	container  *ioc.NestedContainer
	env        *environment.Environment
	envManager environment.Manager
	project    *project.ProjectConfig

	// (this is just the minimal surface we need from a *cobra.Command)
	root interface {
		SetArgs([]string)
		SetContext(context.Context)
		Execute() error
	}
	deployments *recordingProvisioningProvider
	services    *recordingServiceTarget
}

type layerLifecycleTestCase struct {
	name                string
	args                []string
	expectedProvisioned []string
	expectedDestroyed   []string
	expectedServices    []string
	expectedError       string
	serviceOrder        map[string][]string
}

func TestLayerLifecycle_Provision(t *testing.T) {
	tests := []layerLifecycleTestCase{
		{
			name:                "selected layer does not include dependencies",
			args:                []string{"provision", "data", "--no-prompt"},
			expectedProvisioned: []string{"data"},
		},
		{
			name:                "no layer selected just provisions all layers",
			args:                []string{"provision", "--no-prompt"},
			expectedProvisioned: []string{"core", "data"},
		},
		{
			name:                "service-only layer",
			args:                []string{"provision", "app", "--no-prompt"},
			expectedProvisioned: nil, // no resource deployments in this layer
		},
	}

	runLayerLifecycleTests(t, tests)
}

func TestLayerLifecycle_ProvisionMapsAndPersistsOutputs(t *testing.T) {
	const outputMappingProjectYAML = `name: output-mapping-test
infra:
  layers:
    - name: core
      provider: bicep
      path: infra/core
      outputs:
        BACKEND_OUTPUT: MAPPED_BACKEND_OUTPUT
    - name: app
      provider: bicep
      path: infra/app
      dependsOn:
        - core
      inputs:
        INPUT_FOR_APP: MAPPED_BACKEND_OUTPUT
`

	scenario := newLayerLifecycleScenario(t, outputMappingProjectYAML)
	scenario.deployments.outputs = map[string]map[string]provisioning.OutputParameter{
		"core": {
			"BACKEND_OUTPUT": {
				Type:  provisioning.ParameterTypeString,
				Value: "backend",
			},
		},
	}

	require.NoError(t, scenario.run("provision", "--no-prompt"))

	require.Equal(t, "backend", requireEnvVar(t, scenario.env, "MAPPED_BACKEND_OUTPUT"))
	require.Equal(t, "backend", requireEnvVar(t, scenario.env, "ECHOED_INPUT_FOR_APP"))
	requireNoEnvVar(t, scenario.env, "BACKEND_OUTPUT")
	requireNoEnvVar(t, scenario.env, "INPUT_FOR_APP")
	options, has := scenario.deployments.optionsForLayer("app")
	require.True(t, has)
	require.NotContains(t, options.VirtualEnv, "INPUT_FOR_APP",
		"a real output from a completed dependency must not be classified as a plan-only virtual value")

	persistedEnv := environment.New("test")
	require.NoError(t, scenario.envManager.Reload(t.Context(), persistedEnv))

	value := requireEnvVar(t, persistedEnv, "MAPPED_BACKEND_OUTPUT")
	require.Equal(t, "backend", value)

	value = requireEnvVar(t, persistedEnv, "ECHOED_INPUT_FOR_APP")
	require.Equal(t, "backend", value)

	requireNoEnvVar(t, persistedEnv, "BACKEND_OUTPUT")
	requireNoEnvVar(t, persistedEnv, "INPUT_FOR_APP")
}

func TestLayerLifecycle_ProvisionMapsInputsAndOutputsForImplicitLayer(t *testing.T) {
	const projectYAML = `name: implicit-layer-mapping-test
infra:
  provider: bicep
  path: infra
  inputs:
    BACKEND_INPUT: SHARED_BACKEND_INPUT
  outputs:
    BACKEND_OUTPUT: SHARED_BACKEND_OUTPUT
`

	scenario := newLayerLifecycleScenario(t, projectYAML)
	scenario.env.DotenvSet("SHARED_BACKEND_INPUT", "input")
	require.NoError(t, scenario.envManager.Save(t.Context(), scenario.env))
	scenario.deployments.outputs = map[string]map[string]provisioning.OutputParameter{
		"": { // ie, the "implicit" layer
			"BACKEND_OUTPUT": {
				Type:  provisioning.ParameterTypeString,
				Value: "output",
			},
		},
	}

	require.NoError(t, scenario.run("provision", "--no-prompt"))

	require.Equal(t, "input", requireEnvVar(t, scenario.env, "ECHOED_BACKEND_INPUT"))
	require.Equal(t, "output", requireEnvVar(t, scenario.env, "SHARED_BACKEND_OUTPUT"))
	requireNoEnvVar(t, scenario.env, "BACKEND_INPUT")
	requireNoEnvVar(t, scenario.env, "BACKEND_OUTPUT")
	options, has := scenario.deployments.optionsForLayer("")
	require.True(t, has)
	require.NotContains(t, options.VirtualEnv, "BACKEND_INPUT",
		"a real mapped environment value must not be classified as a plan-only virtual value")
}

func TestLayerLifecycle_ProvisionRejectsUnknownOutputMapping(t *testing.T) {
	const projectYAML = `name: invalid-output-mapping-test
infra:
  provider: bicep
  path: infra
  outputs:
    VARIABLE_THAT_IS_NOT_AN_OUTPUT_OOPS: SHARED_BACKEND_OUTPUT
`

	scenario := newLayerLifecycleScenario(t, projectYAML)
	scenario.deployments.outputs = map[string]map[string]provisioning.OutputParameter{
		"": {
			"BACKEND_OUTPUT": {
				Type:  provisioning.ParameterTypeString,
				Value: "output",
			},
		},
	}

	err := scenario.run("provision", "--no-prompt")

	require.ErrorContains(t, err, `output mappings reference unknown provider outputs: VARIABLE_THAT_IS_NOT_AN_OUTPUT_OOPS`)
	require.Empty(t, scenario.deployments.provisionedLayers())
}

// TestLayerLifecycle_ProvisionPreservesConcurrentEnvironmentChanges describes
// the environment merge behavior required when another graph step updates the
// shared environment after a provider-local environment has been created.
func TestLayerLifecycle_ProvisionPreservesConcurrentEnvironmentChanges(t *testing.T) {
	const projectYAML = `name: concurrent-environment-mapping-test
infra:
  provider: bicep
  path: infra
  inputs:
    PROVIDER_INPUT: SHARED_INPUT
`

	scenario := newLayerLifecycleScenario(t, projectYAML)
	scenario.env.DotenvSet("SHARED_INPUT", "input")
	require.NoError(t, scenario.envManager.Save(t.Context(), scenario.env))
	scenario.deployments.deployStarted = make(chan string, 1)
	scenario.deployments.deployRelease = make(chan struct{})
	scenario.deployments.saveProviderEnvironment = true

	result := make(chan error, 1)
	go func() {
		result <- scenario.run("provision", "--no-prompt")
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-scenario.deployments.deployStarted:
	case <-ctx.Done():
		close(scenario.deployments.deployRelease)
		require.FailNow(t, "provider did not start")
	}

	scenario.env.DotenvSet("CONCURRENT_GRAPH_OUTPUT", "preserve-me")
	close(scenario.deployments.deployRelease)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-ctx.Done():
		require.FailNow(t, "provision did not finish after releasing the provider")
	}

	require.Equal(t, "preserve-me", requireEnvVar(t, scenario.env, "CONCURRENT_GRAPH_OUTPUT"))
}

// TestLayerLifecycle_PipelineConfigMapsPlannedOutputs describes how pipeline
// planning must expose an upstream provider output under its shared mapped name
// before resolving a downstream provider-local input.
func TestLayerLifecycle_PipelineConfigMapsPlannedOutputs(t *testing.T) {
	const projectYAML = `name: pipeline-output-mapping-test
infra:
  layers:
    - name: core
      provider: bicep
      path: infra/core
      outputs:
        BACKEND_OUTPUT: SHARED_BACKEND_OUTPUT
    - name: app
      provider: bicep
      path: infra/app
      dependsOn:
        - core
      inputs:
        APP_BACKEND_INPUT: SHARED_BACKEND_OUTPUT
`

	scenario := newLayerLifecycleScenario(t, projectYAML)
	scenario.deployments.outputs = map[string]map[string]provisioning.OutputParameter{
		"core": {
			"BACKEND_OUTPUT": {
				Type:  provisioning.ParameterTypeString,
				Value: "not-used-during-planning",
			},
		},
	}
	scenario.container.MustRegisterScoped(func() pipelineConfigManager {
		return &layerLifecyclePipelineManager{}
	})

	require.NoError(t, scenario.run("pipeline", "config", "--provider", "github", "--no-prompt"))

	options, has := scenario.deployments.optionsForLayer("app")
	require.True(t, has, "pipeline configuration should initialize every infrastructure layer")
	require.Contains(t, options.VirtualEnv, "APP_BACKEND_INPUT",
		"the downstream provider input should resolve from the upstream output's shared mapped name")
}

func TestLayerLifecycle_ProvisionParallelLayerTrees(t *testing.T) {
	scenario := newLayerLifecycleScenario(t, parallelLayerTreesProjectYAML)
	scenario.deployments.deployStarted = make(chan string, 4)
	scenario.deployments.deployRelease = make(chan struct{})

	result := make(chan error, 1)
	go func() {
		result <- scenario.run("provision", "--no-prompt")
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	started := make([]string, 0, 2)
	for range 2 {
		select {
		case name := <-scenario.deployments.deployStarted:
			started = append(started, name)
		case <-ctx.Done():
			close(scenario.deployments.deployRelease)
			require.FailNow(t, "independent layer roots did not start concurrently", "started: %v", started)
		}
	}

	close(scenario.deployments.deployRelease)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-ctx.Done():
		require.FailNow(t, "provision did not finish after releasing layers")
	}
	require.ElementsMatch(t, []string{"core", "monitoring"}, started)
	checkSequencing(t, scenario.deployments.provisionedLayers(), map[string][]string{
		"core":       {"data"},
		"monitoring": {"alerts"},
	})
}

// TestLayerLifecycle_ProvisionRaisesLayerEvents describes what an extension
// author expects after subscribing to layer-level lifecycle events.
func TestLayerLifecycle_ProvisionRaisesLayerEvents(t *testing.T) {
	scenario := newLayerLifecycleScenario(t, layerLifecycleProjectYAML)

	var mu sync.Mutex
	var events []string
	for _, eventName := range []string{"preprovision", "postprovision"} {
		require.NoError(t, scenario.project.LayerEventDispatcher.AddHandler(
			t.Context(),
			ext.Event(eventName),
			func(_ context.Context, args project.LayerLifecycleEventArgs) error {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, eventName+":"+args.Layer.Name)
				return nil
			},
		))
	}

	require.NoError(t, scenario.run("provision", "core", "--no-prompt"))
	require.Equal(t, []string{"preprovision:core", "postprovision:core"}, events)
}

// TestLayerLifecycle_UpReportsExecutionScope describes the selection an
// extension should observe when a user includes transitive layer dependencies.
func TestLayerLifecycle_UpReportsExecutionScope(t *testing.T) {
	scenario := newLayerLifecycleScenario(t, layerLifecycleProjectYAML)

	var mu sync.Mutex
	var scopes []project.ExecutionScope
	require.NoError(t, scenario.project.AddHandler(
		t.Context(),
		ext.Event("preprovision"),
		func(_ context.Context, args project.ProjectLifecycleEventArgs) error {
			scope, ok := args.Args["executionScope"].(project.ExecutionScope)
			if ok {
				mu.Lock()
				scopes = append(scopes, scope)
				mu.Unlock()
			}
			return nil
		},
	))

	require.NoError(t, scenario.run("up", "data", "--include-dependencies", "--no-prompt"))
	require.NotEmpty(t, scopes, "extensions should receive the user's resolved execution scope")
	for _, scope := range scopes {
		require.Equal(t, []string{"data"}, scope.TargetLayers)
		require.Equal(t, []string{"core", "data"}, scope.IncludedLayers)
		require.ElementsMatch(t, []string{"cache", "core-api", "core-worker", "database"}, scope.ServiceNames)
		require.True(t, scope.IncludeDependencies)
	}
}

func TestLayerLifecycle_DeployReportsExecutionScope(t *testing.T) {
	scenario := newLayerLifecycleScenario(t, layerLifecycleProjectYAML)

	var received project.ExecutionScope
	require.NoError(t, scenario.project.AddHandler(
		t.Context(),
		ext.Event("predeploy"),
		func(_ context.Context, args project.ProjectLifecycleEventArgs) error {
			received, _ = args.Args[internalcmd.ProjectEventKeyExecutionScope].(project.ExecutionScope)
			return nil
		},
	))

	require.NoError(t, scenario.run("deploy", "--layer", "app", "--no-prompt"))
	require.Equal(t, []string{"app"}, received.TargetLayers)
	require.Equal(t, []string{"app"}, received.IncludedLayers)
	require.Equal(t, []string{"api", "worker"}, received.ServiceNames)
}

// TestLayerLifecycle_EventEnvelopeSupportsLayerMessages exercises the wire
// contract an extension uses to subscribe to and complete a layer event.
func TestLayerLifecycle_EventEnvelopeSupportsLayerMessages(t *testing.T) {
	envelope := azdext.NewEventMessageEnvelope()
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "example.extension"},
	})

	messages := []struct {
		name      string
		message   *azdext.EventMessage
		requestID string
	}{
		{
			name: "subscribe",
			message: &azdext.EventMessage{MessageType: &azdext.EventMessage_SubscribeLayerEvent{
				SubscribeLayerEvent: &azdext.SubscribeLayerEvent{EventNames: []string{"preprovision"}},
			}},
			requestID: "example.extension.preprovision",
		},
		{
			name: "invoke",
			message: &azdext.EventMessage{MessageType: &azdext.EventMessage_InvokeLayerHandler{
				InvokeLayerHandler: &azdext.InvokeLayerHandler{
					EventName: "preprovision",
					Layer:     &azdext.Layer{Name: "core"},
				},
			}},
			requestID: "example.extension.core.preprovision",
		},
		{
			name: "complete",
			message: &azdext.EventMessage{MessageType: &azdext.EventMessage_LayerHandlerStatus{
				LayerHandlerStatus: &azdext.LayerHandlerStatus{
					EventName: "preprovision",
					LayerName: "core",
				},
			}},
			requestID: "example.extension.core.preprovision",
		},
	}

	for _, test := range messages {
		t.Run(test.name, func(t *testing.T) {
			assert.NotNil(t, envelope.GetInnerMessage(test.message))
			assert.Equal(t, test.requestID, envelope.GetRequestId(ctx, test.message))
		})
	}
}

// TestLayerLifecycle_AddLayerPreservesExistingConfiguration describes the
// read-modify-write behavior an extension expects when updating one layer.
func TestLayerLifecycle_AddLayerPreservesExistingConfiguration(t *testing.T) {
	projectPath := t.TempDir()
	projectFile := filepath.Join(projectPath, azdcontext.ProjectFileName)
	require.NoError(t, os.WriteFile(projectFile, []byte(`name: layer-update-test
infra:
  layers:
    - name: core
      provider: bicep
      path: infra/core
      hooks:
        preprovision:
          shell: sh
          run: ./prepare.sh
      deploymentStacks:
        actionOnUnmanage:
          resources: delete
`), 0o600))

	azdContext := azdcontext.NewAzdContextWithDirectory(projectPath)
	lazyContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return azdContext, nil })
	projectConfig, err := project.Load(t.Context(), projectFile)
	require.NoError(t, err)
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) { return projectConfig, nil })
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", nil), nil
	})
	service := grpcserver.NewProjectService(lazyContext, nil, lazyEnv, lazyProject, project.NewImportManager(nil), nil)

	_, err = service.AddLayer(t.Context(), &azdext.AddLayerRequest{Layer: &azdext.LayerDefinition{
		Name: "core",
		Infra: &azdext.InfraOptions{
			Provider: "bicep",
			Path:     "infra/core-v2",
		},
	}})
	require.NoError(t, err)

	updated, err := project.Load(t.Context(), projectFile)
	require.NoError(t, err)
	require.Len(t, updated.Infra.Layers, 1)
	t.Run("hooks", func(t *testing.T) {
		require.NotEmpty(t, updated.Infra.Layers[0].Hooks, "updating a layer should preserve its hooks")
	})
	t.Run("deployment stacks", func(t *testing.T) {
		require.NotNil(
			t,
			updated.Infra.Layers[0].DeploymentStacks,
			"updating a layer should preserve its deployment stack settings",
		)
	})
}

func TestLayerLifecycle_Up(t *testing.T) {
	tests := []layerLifecycleTestCase{
		{
			name:                "selected layer",
			args:                []string{"up", "core", "--no-prompt"},
			expectedProvisioned: []string{"core"},
			expectedServices:    []string{"core-api", "core-worker"},
		},
		{
			name:                "layer dependencies",
			args:                []string{"up", "data", "--include-dependencies", "--no-prompt"},
			expectedProvisioned: []string{"core", "data"},
			expectedServices:    []string{"core-api", "core-worker", "database", "cache"},
			serviceOrder: map[string][]string{
				"core-api":    {"database", "cache"},
				"core-worker": {"database", "cache"},
			},
		},
		{
			name:                "service uses across layers",
			args:                []string{"up", "app", "--include-dependencies", "--no-prompt"},
			expectedProvisioned: []string{"core", "data"},
			expectedServices:    []string{"core-api", "core-worker", "database", "cache", "api", "worker"},
			serviceOrder: map[string][]string{
				"core-api":    {"database", "cache"},
				"core-worker": {"database", "cache"},
				"database":    {"api", "worker"},
				"cache":       {"api", "worker"},
			},
		},
		{
			name:                "service depends on layer",
			args:                []string{"up", "jobs", "--include-dependencies", "--no-prompt"},
			expectedProvisioned: []string{"core"},
			expectedServices:    []string{"core-api", "core-worker", "job", "scheduler"},
			serviceOrder: map[string][]string{
				"core-api":    {"job", "scheduler"},
				"core-worker": {"job", "scheduler"},
			},
		},
	}

	runLayerLifecycleTests(t, tests)
}

func TestLayerLifecycle_Deploy(t *testing.T) {
	tests := []layerLifecycleTestCase{
		{
			name:             "all services honor layer dependencies",
			args:             []string{"deploy", "--all", "--no-prompt"},
			expectedServices: []string{"core-api", "core-worker", "database", "cache", "api", "worker", "job", "scheduler"},
			serviceOrder: map[string][]string{
				"core-api":    {"database", "cache", "api", "worker", "job", "scheduler"},
				"core-worker": {"database", "cache", "api", "worker", "job", "scheduler"},
				"database":    {"api", "worker"},
				"cache":       {"api", "worker"},
			},
		},
		{
			name:             "selected layer",
			args:             []string{"deploy", "--layer", "app", "--no-prompt"},
			expectedServices: []string{"api", "worker"},
		},
		{
			name:             "service in layer",
			args:             []string{"deploy", "api", "--layer", "app", "--no-prompt"},
			expectedServices: []string{"api"},
		},
		{
			name:          "service outside layer",
			args:          []string{"deploy", "core-api", "--layer", "app", "--no-prompt"},
			expectedError: `service "core-api" does not belong to layer "app"`,
		},
	}

	runLayerLifecycleTests(t, tests)
}

func TestLayerLifecycle_Down(t *testing.T) {
	tests := []layerLifecycleTestCase{
		{
			name:              "all layers in reverse order",
			args:              []string{"down", "--force", "--no-prompt"},
			expectedDestroyed: []string{"data", "core"},
		},
		{
			name:              "selected layer",
			args:              []string{"down", "core", "--force", "--no-prompt"},
			expectedDestroyed: []string{"core"},
		},
		{
			name: "service-only layer",
			args: []string{"down", "app", "--force", "--no-prompt"},
			// today we don't have a 'delete services'
			expectedError: `layer "app" has no infrastructure to delete`,
		},
	}

	runLayerLifecycleTests(t, tests)
}

func newLayerLifecycleScenario(t *testing.T, azureYAML string) *layerLifecycleScenario {
	t.Helper()

	projectPath := t.TempDir()
	writeLayerLifecycleProject(t, projectPath, azureYAML)
	t.Chdir(projectPath)
	t.Setenv("AZURE_ENV_NAME", "test")

	mockContext := mocks.NewMockContext(t.Context())
	globalOptions := &internal.GlobalCommandOptions{
		Cwd:             projectPath,
		EnvironmentName: "test",
		NoPrompt:        true,
	}
	registerCommonDependencies(mockContext.Container)

	_, err := platform.Initialize(mockContext.Container, azd.PlatformKindDefault)
	require.NoError(t, err)

	ioc.RegisterInstance[policy.Transporter](mockContext.Container, mockContext.HttpClient)
	ioc.RegisterInstance[auth.HttpClient](mockContext.Container, mockContext.HttpClient)
	ioc.RegisterInstance[config.FileConfigManager](mockContext.Container, mockContext.ConfigManager)
	ioc.RegisterInstance(
		mockContext.Container,
		config.NewUserConfigManager(mockContext.ConfigManager),
	)
	ioc.RegisterInstance(mockContext.Container, t.Context())
	ioc.RegisterInstance(mockContext.Container, globalOptions)
	ioc.RegisterInstance[internalcmd.EnvironmentDetailsProvider](mockContext.Container, lifecycleEnvironmentDetails{})

	deployments := &recordingProvisioningProvider{}
	services := &recordingServiceTarget{}
	mockContext.Container.MustRegisterNamedTransient(string(provisioning.Bicep), func(
		env *environment.Environment,
		envManager environment.Manager,
	) provisioning.Provider {
		return &layerLifecycleProvisioningProvider{
			recorder:   deployments,
			env:        env,
			envManager: envManager,
		}
	})
	ioc.RegisterNamedInstance[project.ServiceTarget](mockContext.Container, "test", services)

	mockContext.Container.MustRegisterScoped(func() middleware.CurrentUserAuthManager {
		return &lifecycleAuthManager{credential: mockContext.Credentials}
	})
	var azdContext *azdcontext.AzdContext
	require.NoError(t, mockContext.Container.Resolve(&azdContext))
	require.NoError(t, azdContext.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))
	root := newRootCmdWithoutRegistration(mockContext.Container)

	var projectConfig *project.ProjectConfig
	require.NoError(t, mockContext.Container.Resolve(&projectConfig))

	var envManager environment.Manager
	require.NoError(t, mockContext.Container.Resolve(&envManager))
	env, err := envManager.Create(t.Context(), environment.Spec{Name: "test"})
	require.NoError(t, err)
	env.SetSubscriptionId(lifecycleTestSubscription)
	env.SetLocation("not actually used")
	env.DotenvSet("AZURE_RESOURCE_GROUP", "rg-never-actually-deployed")
	require.NoError(t, envManager.Save(t.Context(), env))
	env, err = envManager.Get(t.Context(), env.Name())
	require.NoError(t, err)

	return &layerLifecycleScenario{
		t:           t,
		container:   mockContext.Container,
		env:         env,
		envManager:  envManager,
		project:     projectConfig,
		root:        root,
		deployments: deployments,
		services:    services,
	}
}

type layerLifecyclePipelineManager struct{}

func (*layerLifecyclePipelineManager) CiProviderName() string {
	return "test"
}

func (*layerLifecyclePipelineManager) SetParameters([]provisioning.Parameter) {}

func (*layerLifecyclePipelineManager) Configure(
	context.Context,
	string,
	*project.Infra,
) (*pipeline.PipelineConfigResult, error) {
	return &pipeline.PipelineConfigResult{
		RepositoryLink: "https://example.invalid/repository",
		PipelineLink:   "https://example.invalid/pipeline",
	}, nil
}

func writeLayerLifecycleProject(t *testing.T, projectPath, azureYAML string) {
	t.Helper()

	projectFile := filepath.Join(projectPath, azdcontext.ProjectFileName)
	require.NoError(t, os.WriteFile(projectFile, []byte(azureYAML), 0o600))

	projectConfig, err := project.Load(t.Context(), projectFile)
	require.NoError(t, err)
	for _, layer := range projectConfig.Infra.Layers {
		options, err := layer.GetWithDefaults(projectConfig.Infra)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(options.AbsolutePath(projectPath), 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(options.AbsolutePath(projectPath), options.Module+".bicep"),
			[]byte("targetScope = 'subscription'\n"),
			0o600,
		))
	}
}

func (s *layerLifecycleScenario) run(args ...string) error {
	s.t.Helper()
	s.root.SetArgs(args)
	s.root.SetContext(s.t.Context())
	return s.root.Execute()
}

func runLayerLifecycleTests(t *testing.T, tests []layerLifecycleTestCase) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := newLayerLifecycleScenario(t, layerLifecycleProjectYAML)

			err := scenario.run(test.args...)

			if test.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.expectedError)
			}
			require.Equal(t, test.expectedProvisioned, scenario.deployments.provisionedLayers())
			require.Equal(t, test.expectedDestroyed, scenario.deployments.destroyedLayers())

			deployed := scenario.services.deployedServices()
			checkSequencing(t, deployed, test.serviceOrder)
		})
	}
}

type lifecycleAuthManager struct {
	credential azcore.TokenCredential
}

type lifecycleEnvironmentDetails struct{}

func (lifecycleEnvironmentDetails) GetSubscription(
	_ context.Context,
	subscriptionId string,
) (*account.Subscription, error) {
	return &account.Subscription{Id: subscriptionId, Name: "Layer lifecycle test"}, nil
}

func (lifecycleEnvironmentDetails) GetLocation(
	_ context.Context,
	_ string,
	locationName string,
) (account.Location, error) {
	return account.Location{Name: locationName, DisplayName: locationName}, nil
}

type recordingServiceTarget struct {
	project.ServiceTarget

	mu       sync.Mutex
	deployed []string
}

func (t *recordingServiceTarget) Initialize(context.Context, *project.ServiceConfig) error {
	return nil
}

func (t *recordingServiceTarget) RequiredExternalTools(
	context.Context,
	*project.ServiceConfig,
) []tools.ExternalTool {
	return nil
}

func (t *recordingServiceTarget) ResolveTargetResource(
	_ context.Context,
	subscriptionID string,
	service *project.ServiceConfig,
	_ func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	return environment.NewTargetResource(
		subscriptionID,
		"rg-layer-lifecycle-test",
		service.Name,
		"test",
	), nil
}

func (t *recordingServiceTarget) Package(
	_ context.Context,
	service *project.ServiceConfig,
	_ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServicePackageResult, error) {
	return &project.ServicePackageResult{}, nil
}

func (t *recordingServiceTarget) Publish(
	_ context.Context,
	service *project.ServiceConfig,
	_ *project.ServiceContext,
	_ *environment.TargetResource,
	_ *async.Progress[project.ServiceProgress],
	_ *project.PublishOptions,
) (*project.ServicePublishResult, error) {
	return &project.ServicePublishResult{}, nil
}

func (t *recordingServiceTarget) Deploy(
	_ context.Context,
	service *project.ServiceConfig,
	_ *project.ServiceContext,
	_ *environment.TargetResource,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deployed = append(t.deployed, service.Name)
	return &project.ServiceDeployResult{}, nil
}

func (t *recordingServiceTarget) Endpoints(
	context.Context,
	*project.ServiceConfig,
	*environment.TargetResource,
) ([]string, error) {
	return nil, nil
}

func (t *recordingServiceTarget) deployedServices() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.deployed)
}

// checkSequencing checks that each predecessor appears before its successors.
// ordering: map of 'predecessor' to 'items it must preceed'. This lets us do comparisons
// where multiple branches of things happen in parallel, like service deployments:
//
// For example:
//
//	serviceOrder: map[string][]string{
//	  "core-api":    {"database", "cache"},
//	  "core-worker": {"database", "cache"},
//	  "database":    {"api", "worker"},
//	  "cache":       {"api", "worker"},
//	}
//
// So this map is saying "core-api" has to deploy before database and cache (but database and cache
// might deploy in either order), and _all_ keys could _also_ deploy in any order.
func checkSequencing(t *testing.T, actual []string, ordering map[string][]string) {
	t.Helper()

	indices := make(map[string]int, len(actual))

	for i, name := range actual {
		indices[name] = i
	}

	for first, after := range ordering {
		firstIdx, exists := indices[first]
		require.Truef(t, exists, "%s wasn't expected - no item in actual corresponds to that name", first)

		require.Subset(t, actual[firstIdx+1:], after)
	}
}

func (m *lifecycleAuthManager) Cloud() *cloud.Cloud {
	return cloud.AzurePublic()
}

func (m *lifecycleAuthManager) Mode() (auth.AuthSource, error) {
	return auth.AzdBuiltIn, nil
}

func (m *lifecycleAuthManager) CredentialForCurrentUser(
	context.Context,
	*auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	return m.credential, nil
}

type recordingProvisioningProvider struct {
	mu                      sync.Mutex
	provisionedNames        []string
	destroyedNames          []string
	options                 map[string]provisioning.Options
	outputs                 map[string]map[string]provisioning.OutputParameter
	deployStarted           chan string
	deployRelease           chan struct{}
	saveProviderEnvironment bool
}

func (p *recordingProvisioningProvider) recordOptions(options provisioning.Options) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.options == nil {
		p.options = map[string]provisioning.Options{}
	}
	options.VirtualEnv = maps.Clone(options.VirtualEnv)
	p.options[options.Name] = options
}

func (p *recordingProvisioningProvider) optionsForLayer(layerName string) (provisioning.Options, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	options, has := p.options[layerName]
	return options, has
}

func (p *recordingProvisioningProvider) recordProvisioned(layerName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provisionedNames = append(p.provisionedNames, layerName)
}

func (p *recordingProvisioningProvider) provisionedLayers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.provisionedNames)
}

func (p *recordingProvisioningProvider) recordDestroyed(layerName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.destroyedNames = append(p.destroyedNames, layerName)
}

func (p *recordingProvisioningProvider) destroyedLayers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.destroyedNames)
}

type layerLifecycleProvisioningProvider struct {
	recorder   *recordingProvisioningProvider
	env        *environment.Environment
	envManager environment.Manager
	options    provisioning.Options
}

func (p *layerLifecycleProvisioningProvider) Name() string {
	return "layer lifecycle test"
}

func (p *layerLifecycleProvisioningProvider) Initialize(
	_ context.Context,
	_ string,
	options provisioning.Options,
) error {
	p.options = options
	p.recorder.recordOptions(options)
	return nil
}

func (p *layerLifecycleProvisioningProvider) State(
	context.Context,
	*provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return &provisioning.StateResult{State: &provisioning.State{
		Outputs:   map[string]provisioning.OutputParameter{},
		Resources: []provisioning.Resource{},
	}}, nil
}

func (p *layerLifecycleProvisioningProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	if p.recorder.deployStarted != nil {
		p.recorder.deployStarted <- p.options.Name
		select {
		case <-p.recorder.deployRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.recorder.saveProviderEnvironment {
		if err := p.envManager.Save(ctx, p.env); err != nil {
			return nil, err
		}
	}

	p.recorder.recordProvisioned(p.options.Name)

	outputs := maps.Clone(p.recorder.outputs[p.options.Name])
	if outputs == nil {
		outputs = map[string]provisioning.OutputParameter{}
	}
	for providerInput := range p.options.Inputs {
		if value, has := p.env.LookupEnv(providerInput); has {
			// Echo mapped inputs as outputs so integration tests can observe the complete mapping pipeline.
			outputs["ECHOED_"+providerInput] = provisioning.OutputParameter{
				Type:  provisioning.ParameterTypeString,
				Value: value,
			}
		}
	}
	return &provisioning.DeployResult{Deployment: &provisioning.Deployment{
		Parameters: map[string]provisioning.InputParameter{},
		Outputs:    outputs,
	}}, nil
}

func (p *layerLifecycleProvisioningProvider) Preview(context.Context) (*provisioning.DeployPreviewResult, error) {
	return &provisioning.DeployPreviewResult{Preview: &provisioning.DeploymentPreview{}}, nil
}

func (p *layerLifecycleProvisioningProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	p.recorder.recordDestroyed(p.options.Name)
	return &provisioning.DestroyResult{}, nil
}

func (p *layerLifecycleProvisioningProvider) EnsureEnv(context.Context) error {
	return nil
}

func (p *layerLifecycleProvisioningProvider) Parameters(context.Context) ([]provisioning.Parameter, error) {
	return nil, nil
}

func (p *layerLifecycleProvisioningProvider) PlannedOutputs(context.Context) ([]provisioning.PlannedOutput, error) {

	outputs := p.recorder.outputs[p.options.Name]
	planned := make([]provisioning.PlannedOutput, 0, len(outputs))

	for name := range outputs {
		planned = append(planned, provisioning.PlannedOutput{Name: name})
	}

	return planned, nil
}

func requireNoEnvVar(t *testing.T, env *environment.Environment, name string) {
	_, exists := env.LookupEnv(name)
	require.Falsef(t, exists, "%s should not be in the environment", name)
}

func requireEnvVar(t *testing.T, env *environment.Environment, name string) string {
	v, exists := env.LookupEnv(name)
	require.Truef(t, exists, "%s should be in the environment", name)
	return v
}

var _ provisioning.Provider = (*layerLifecycleProvisioningProvider)(nil)
var _ middleware.CurrentUserAuthManager = (*lifecycleAuthManager)(nil)
