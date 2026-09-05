// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// providerEnvironmentManager prevents infra-entry-scoped input aliases from being
// persisted while preserving other environment changes made by the entry's provider.
type providerEnvironmentManager struct {
	// Manager owns the caller's save boundary. The parallel provision graph uses
	// a non-persisting manager and reconciles the layer environment later.
	environment.Manager
	// baseEnv is the environment supplied to this provisioning manager. It may be
	// the command environment or an isolated per-layer clone.
	baseEnv *environment.Environment
	// initialValues is a snapshot of the infra entry's environment created from
	// baseEnv before provider execution. It includes input aliases derived from
	// existing .env values and is used to identify the provider's delta.
	initialValues map[string]string
	// initialConfig is the infra entry's config baseline used to reconcile only
	// provider changes back into the owning environment.
	initialConfig config.Config
	// inputs maps aliases used by this infra entry's provider to shared azd environment
	// keys. Alias keys are excluded when synchronizing provider changes back into baseEnv.
	inputs map[string]string
	// providerEnv contains the provider-local projection of baseEnv, including input aliases.
	providerEnv *environment.Environment
}

// newProviderEnvironmentManager creates a provider-local environment, applies the entry's
// persisted and virtual input aliases, and captures the baseline used to synchronize
// provider changes without allowing those aliases to escape the scope.
//
// It returns a copy of options containing the provider-local VirtualEnv aliases;
// callers must use the returned copy when initializing the provider.
func newProviderEnvironmentManager(
	manager environment.Manager,
	baseEnv *environment.Environment,
	options Options,
) (*providerEnvironmentManager, Options) {
	providerEnv := environment.NewWithValues(baseEnv.Name(), baseEnv.Dotenv())
	providerEnv.Config = config.Clone(baseEnv.Config)
	options.VirtualEnv = maps.Clone(options.VirtualEnv)

	for providerInput, environmentKey := range options.Inputs {
		if value, has := options.VirtualEnv[environmentKey]; has {
			options.VirtualEnv[providerInput] = value
			continue
		}

		if value, has := baseEnv.LookupEnv(environmentKey); has {
			providerEnv.DotenvSet(providerInput, value)
		}
	}

	return &providerEnvironmentManager{
		Manager:       manager,
		baseEnv:       baseEnv,
		initialValues: providerEnv.Dotenv(),
		initialConfig: config.Clone(baseEnv.Config),
		inputs:        options.Inputs,
		providerEnv:   providerEnv,
	}, options
}

func (m *providerEnvironmentManager) environment() *environment.Environment {
	return m.providerEnv
}

func (m *providerEnvironmentManager) Save(ctx context.Context, providerEnv *environment.Environment) error {
	m.sync(providerEnv)
	return m.Manager.Save(ctx, m.baseEnv)
}

func (m *providerEnvironmentManager) SaveWithOptions(
	ctx context.Context,
	providerEnv *environment.Environment,
	options *environment.SaveOptions,
) error {
	m.sync(providerEnv)
	return m.Manager.SaveWithOptions(ctx, m.baseEnv, options)
}

func (m *providerEnvironmentManager) sync(providerEnv *environment.Environment) {
	ourLayerValues := providerEnv.Dotenv()

	initialConfig := m.applyDotEnvDelta(ourLayerValues)
	config.ApplyDelta(m.baseEnv.Config, initialConfig, providerEnv.Config)
}

// applyDotEnvDelta looks at changes we've made in our local layer map, and applies them back
// to the environment. The critical parts:
// - Deletions in our local map are reflected in dotenv
// - Updates in our local map are reflected in dotenv
// - Unchanged values are not set again
func (m *providerEnvironmentManager) applyDotEnvDelta(ourLayerValues map[string]string) config.Config {
	for key, initialValue := range m.initialValues {
		if _, isInputAlias := m.inputs[key]; isInputAlias {
			// Input aliases exist only for this infra entry, never propagate them.
			continue
		}

		// we're taking our layer values as the truth - deleting any keys that no longer
		// exist, and setting keys with _our_ values.
		value, keep := ourLayerValues[key]
		if !keep {
			m.baseEnv.DotenvDelete(key)
		} else if value != initialValue {
			m.baseEnv.DotenvSet(key, value)
		}
	}

	// Values absent from the initial snapshot were created by the provider and
	// need to be copied back. Existing keys were handled above; skipping them
	// here avoids replacing a concurrent shared-environment update with an
	// unchanged value from the provider's stale snapshot.
	for key, value := range ourLayerValues {
		if _, isInput := m.inputs[key]; isInput {
			// Input aliases exist only for this infra entry, never propagate them.
			continue
		}

		if _, existedInitially := m.initialValues[key]; !existedInitially {
			m.baseEnv.DotenvSet(key, value)
		}
	}

	initialConfig := m.initialConfig
	if initialConfig == nil {
		initialConfig = config.Clone(m.baseEnv.Config)
	}
	return initialConfig
}

// validateLayerOutputMappings makes sure the user's mapped outputs actually match
// a variable in the planned outputs. Helps them avoid typos.
func validateLayerOutputMappings(
	outputs map[string]OutputParameter,
	outputMappings map[string]string,
) error {
	if len(outputMappings) == 0 {
		return nil
	}

	var missing []string
	for _, providerOutput := range slices.Sorted(maps.Keys(outputMappings)) {
		if _, has := outputs[providerOutput]; !has {
			missing = append(missing, providerOutput)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	available := slices.Sorted(maps.Keys(outputs))
	return fmt.Errorf(
		"output mappings reference unknown provider outputs: %s; available outputs: %s",
		strings.Join(missing, ", "),
		strings.Join(available, ", "),
	)
}

func plannedOutputParameters(outputs []PlannedOutput) map[string]OutputParameter {
	parameters := make(map[string]OutputParameter, len(outputs))
	for _, output := range outputs {
		parameters[output.Name] = OutputParameter{}
	}
	return parameters
}

func applyPlannedOutputMappings(outputs []PlannedOutput, mappings map[string]string) ([]PlannedOutput, error) {
	mapped := make([]PlannedOutput, len(outputs))
	owners := make(map[string]string, len(outputs))
	for i, output := range outputs {
		name := output.Name
		if configured, has := mappings[name]; has {
			name = configured
		}
		if owner, has := owners[name]; has && owner != output.Name {
			return nil, fmt.Errorf(
				"provider outputs %q and %q both map to environment key %q",
				owner, output.Name, name,
			)
		}
		owners[name] = output.Name
		mapped[i] = PlannedOutput{Name: name}
	}
	return mapped, nil
}

// applyLayerOutputMappings maps provider output names to shared azd environment
// keys. It rejects runtime collisions when two provider outputs resolve to the
// same shared key.
func applyLayerOutputMappings(
	outputs map[string]OutputParameter,
	mappings map[string]string,
) (map[string]OutputParameter, error) {
	if len(outputs) == 0 {
		return outputs, nil
	}

	mapped := make(map[string]OutputParameter, len(outputs))
	owners := make(map[string]string, len(outputs))
	for providerOutput, parameter := range outputs {
		environmentKey := providerOutput
		if configured, has := mappings[providerOutput]; has {
			if configured == "" {
				return nil, fmt.Errorf("provider output %q maps to an empty environment key", providerOutput)
			}
			environmentKey = configured
		}
		// Track the original output name because assigning directly to mapped
		// would otherwise silently replace an output mapped to the same key.
		if owner, has := owners[environmentKey]; has && owner != providerOutput {
			return nil, fmt.Errorf(
				"provider outputs %q and %q both map to environment key %q",
				owner, providerOutput, environmentKey,
			)
		}
		owners[environmentKey] = providerOutput
		mapped[environmentKey] = parameter
	}
	return mapped, nil
}

// applyLayerOutputKeyMappings maps provider invalidation keys to the shared azd
// environment keys used for persisted outputs.
func applyLayerOutputKeyMappings(keys []string, mappings map[string]string) []string {
	if len(keys) == 0 {
		return keys
	}
	mapped := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if configured, has := mappings[key]; has && configured != "" {
			key = configured
		}
		mapped[key] = struct{}{}
	}
	return slices.Sorted(maps.Keys(mapped))
}
