// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// ExecutionScope describes the project layers and services selected for one command.
type ExecutionScope struct {
	TargetLayers        []string
	IncludedLayers      []string
	ServiceNames        []string
	IncludeDependencies bool
}

// NewExecutionScope creates a deterministic command scope from the layers selected for execution.
func NewExecutionScope(
	targetLayers []string,
	includedLayers []*Layer,
	includeDependencies bool,
) ExecutionScope {
	includedNames := make([]string, 0, len(includedLayers))
	var serviceNames []string
	for _, layer := range includedLayers {
		includedNames = append(includedNames, layer.Name)
		for _, service := range layer.Services {
			serviceNames = append(serviceNames, service.Name)
		}
	}
	slices.Sort(includedNames)
	slices.Sort(serviceNames)
	return ExecutionScope{
		TargetLayers:        slices.Clone(targetLayers),
		IncludedLayers:      includedNames,
		ServiceNames:        serviceNames,
		IncludeDependencies: includeDependencies,
	}
}

// LayerLifecycleEventArgs are supplied to layer lifecycle event handlers.
type LayerLifecycleEventArgs struct {
	Project *ProjectConfig
	Layer   *Layer
	Scope   ExecutionScope
}

// RaiseLayerEvent raises a layer lifecycle event when a dispatcher is configured.
// Project configurations constructed directly by callers may not have runtime
// dispatchers initialized, in which case there are no handlers to invoke.
func (pc *ProjectConfig) RaiseLayerEvent(ctx context.Context, name ext.Event, args LayerLifecycleEventArgs) error {
	if pc.LayerEventDispatcher == nil {
		return nil
	}
	return pc.LayerEventDispatcher.RaiseEvent(ctx, name, args)
}

// ErrLayerNotFound indicates that a requested logical project layer does not exist.
var ErrLayerNotFound = errors.New("project layer not found")

// Layer is the resolved view of one logical project layer.
//
// A layer is inferred from the union of infrastructure layer definitions and
// service layer assignments. Infra is nil for a service-only layer. The
// implicit legacy layer has an empty Name and Implicit set to true.
type Layer struct {
	Name      string
	Infra     *provisioning.Options
	Services  []*ServiceConfig
	DependsOn []string
	Inputs    map[string]string
	Outputs   map[string]string
	Implicit  bool
}

// GetLayer resolves one logical project layer by name. An empty name resolves
// the implicit legacy layer. Inferred service-only layers have no durable
// definition of their own and cease to exist when their final service is moved
// or removed.
func (im *ImportManager) GetLayer(
	ctx context.Context,
	projectConfig *ProjectConfig,
	name string,
) (*Layer, error) {
	layers, err := im.ListLayers(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	for _, layer := range layers {
		if layer.Name == name {
			return layer, nil
		}
	}

	available := make([]string, len(layers))
	for i, layer := range layers {
		available[i] = layer.Name
		if layer.Implicit {
			available[i] = "<implicit>"
		}
	}

	return nil, fmt.Errorf("%w: %q (available layers: %s)", ErrLayerNotFound, name, strings.Join(available, ", "))
}

// ListLayers resolves all logical project layers, including inferred
// service-only layers and the implicit legacy layer when it has members.
// Inferred service-only layers cannot independently persist hooks, mappings,
// configuration, or explicit dependencies.
func (im *ImportManager) ListLayers(
	ctx context.Context,
	projectConfig *ProjectConfig,
) ([]*Layer, error) {
	if projectConfig == nil {
		return nil, errors.New("project config is nil")
	}

	services, err := im.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	layersByName := map[string]*Layer{}
	ensureLayer := func(name string) *Layer {
		if layer, has := layersByName[name]; has {
			return layer
		}

		layer := &Layer{Name: name, Implicit: name == ""}
		layersByName[name] = layer
		return layer
	}

	if len(projectConfig.Infra.Layers) == 0 {
		implicit := ensureLayer("")
		implicit.Infra = new(projectConfig.Infra)
		implicit.Inputs = maps.Clone(projectConfig.Infra.Inputs)
		implicit.Outputs = maps.Clone(projectConfig.Infra.Outputs)
	}

	for i := range projectConfig.Infra.Layers {
		infra := projectConfig.Infra.Layers[i]
		if infra.Name == "" {
			return nil, errors.New("infrastructure layer name cannot be empty")
		}
		if _, has := layersByName[infra.Name]; has {
			return nil, fmt.Errorf("duplicate infrastructure layer %q", infra.Name)
		}

		layer := ensureLayer(infra.Name)
		layer.Infra = new(infra)
		layer.Inputs = maps.Clone(infra.Inputs)
		layer.Outputs = maps.Clone(infra.Outputs)
	}

	servicesByName := make(map[string]*ServiceConfig, len(services))
	for _, service := range services {
		servicesByName[service.Name] = service
		layer := ensureLayer(service.Layer)
		layer.Services = append(layer.Services, service)
	}

	dependencySets := make(map[string]map[string]struct{}, len(layersByName))
	for name := range layersByName {
		dependencySets[name] = map[string]struct{}{}
	}

	for _, layer := range layersByName {
		if layer.Infra == nil {
			continue
		}
		for _, dependency := range layer.Infra.DependsOn {
			if dependency == layer.Name {
				return nil, fmt.Errorf("layer %q cannot depend on itself", layer.Name)
			}
			if _, has := layersByName[dependency]; !has {
				return nil, fmt.Errorf("layer %q depends on unknown layer %q", layer.Name, dependency)
			}
			dependencySets[layer.Name][dependency] = struct{}{}
		}
	}

	for _, service := range services {
		for _, dependencyLayer := range service.DependsOn {
			if dependencyLayer == service.Layer {
				return nil, fmt.Errorf(
					"service %q cannot depend on its own layer %q",
					service.Name, service.Layer,
				)
			}
			if _, has := layersByName[dependencyLayer]; !has {
				return nil, fmt.Errorf(
					"service %q depends on unknown layer %q",
					service.Name, dependencyLayer,
				)
			}
			dependencySets[service.Layer][dependencyLayer] = struct{}{}
		}
		for _, dependencyName := range service.Uses {
			dependency, isService := servicesByName[dependencyName]
			if !isService || dependency.Layer == service.Layer {
				continue
			}
			dependencySets[service.Layer][dependency.Layer] = struct{}{}
		}
	}

	if err := validateLayerDependencies(dependencySets); err != nil {
		return nil, err
	}

	outputOwners := map[string]string{}
	for layerName, layer := range layersByName {
		for providerOutput, environmentKey := range layer.Outputs {
			if environmentKey == "" {
				return nil, fmt.Errorf("layer %q output %q maps to an empty environment key", layerName, providerOutput)
			}
			if owner, has := outputOwners[environmentKey]; has && owner != layerName {
				return nil, fmt.Errorf(
					"environment output %q is owned by both layers %q and %q",
					environmentKey, owner, layerName,
				)
			}
			outputOwners[environmentKey] = layerName
		}
	}

	for name, dependencies := range dependencySets {
		layersByName[name].DependsOn = slices.Sorted(maps.Keys(dependencies))
	}

	names := slices.Sorted(maps.Keys(layersByName))
	layers := make([]*Layer, 0, len(names))
	for _, name := range names {
		layers = append(layers, layersByName[name])
	}
	return layers, nil
}

func validateLayerDependencies(dependencies map[string]map[string]struct{}) error {
	const (
		unvisited = iota
		visiting
		visited
	)

	states := make(map[string]int, len(dependencies))
	var visit func(string) error
	visit = func(name string) error {
		switch states[name] {
		case visiting:
			return fmt.Errorf("circular dependency detected at layer %q", name)
		case visited:
			return nil
		}

		states[name] = visiting
		for _, dependency := range slices.Sorted(maps.Keys(dependencies[name])) {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		states[name] = visited
		return nil
	}

	for _, name := range slices.Sorted(maps.Keys(dependencies)) {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// SelectLayers returns the named layer, optionally including its transitive
// dependencies. Results preserve the deterministic order from ListLayers.
func SelectLayers(layers []*Layer, name string, includeDependencies bool) ([]*Layer, error) {
	byName := make(map[string]*Layer, len(layers))
	for _, layer := range layers {
		byName[layer.Name] = layer
	}
	if _, has := byName[name]; !has {
		return nil, fmt.Errorf("%w: %q", ErrLayerNotFound, name)
	}

	selected := map[string]struct{}{name: {}}
	if includeDependencies {
		var include func(string)
		include = func(current string) {
			for _, dependency := range byName[current].DependsOn {
				if _, has := selected[dependency]; has {
					continue
				}
				selected[dependency] = struct{}{}
				include(dependency)
			}
		}
		include(name)
	}

	result := make([]*Layer, 0, len(selected))
	for _, layer := range layers {
		if _, has := selected[layer.Name]; has {
			result = append(result, layer)
		}
	}
	return result, nil
}
