// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AddLayer adds or updates an infrastructure-backed project layer.
func (s *projectService) AddLayer(
	ctx context.Context,
	req *azdext.AddLayerRequest,
) (*azdext.LayerResponse, error) {
	if req.GetLayer() == nil || req.GetLayer().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	if req.GetLayer().GetInfra() == nil {
		return nil, status.Error(codes.InvalidArgument, "layer infrastructure cannot be empty")
	}

	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.reloadProjectForMutation(ctx)
	if err != nil {
		return nil, err
	}

	if len(projectConfig.Infra.Layers) == 0 {
		if err := ensureLayerCreationIsSafe(ctx, projectFilePath, projectConfig.Path); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	}

	var infra provisioning.Options
	if err := mapper.Convert(req.GetLayer().GetInfra(), &infra); err != nil {
		return nil, fmt.Errorf("converting layer infrastructure: %w", err)
	}
	infra.Name = req.GetLayer().GetName()
	infra.DependsOn = slices.Clone(req.GetLayer().GetDependsOn())
	infra.Inputs = cloneStringMap(req.GetLayer().GetInputs())
	infra.Outputs = cloneStringMap(req.GetLayer().GetOutputs())

	if index := slices.IndexFunc(projectConfig.Infra.Layers, func(layer provisioning.Options) bool {
		return layer.Name == infra.Name
	}); index >= 0 {
		infra.Hooks = projectConfig.Infra.Layers[index].Hooks
		infra.DeploymentStacks = projectConfig.Infra.Layers[index].DeploymentStacks
		projectConfig.Infra.Layers[index] = infra
	} else {
		projectConfig.Infra.Layers = append(projectConfig.Infra.Layers, infra)
	}

	if err := projectConfig.Infra.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	resolved, err := s.layerImportManager().GetLayer(ctx, projectConfig, infra.Name)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := project.Save(ctx, projectConfig, projectFilePath); err != nil {
		return nil, err
	}

	return s.layerResponse(resolved)
}

// GetLayer gets one resolved project layer.
func (s *projectService) GetLayer(
	ctx context.Context,
	req *azdext.GetLayerRequest,
) (*azdext.LayerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	layer, err := s.layerImportManager().GetLayer(ctx, projectConfig, req.GetName())
	if errors.Is(err, project.ErrLayerNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return s.layerResponse(layer)
}

// ListLayers lists all resolved project layers.
func (s *projectService) ListLayers(
	ctx context.Context,
	_ *azdext.EmptyRequest,
) (*azdext.ListLayersResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	layers, err := s.layerImportManager().ListLayers(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	response := &azdext.ListLayersResponse{Layers: make([]*azdext.Layer, len(layers))}
	for i, layer := range layers {
		mapped, err := s.layerToProto(layer)
		if err != nil {
			return nil, err
		}
		response.Layers[i] = mapped
	}
	return response, nil
}

// RemoveLayer removes a project layer using the requested safety mode.
func (s *projectService) RemoveLayer(
	ctx context.Context,
	req *azdext.RemoveLayerRequest,
) (*azdext.RemoveLayerResponse, error) {
	if req == nil || req.GetName() == "" {
		return nil, status.Error(codes.FailedPrecondition, "the implicit layer cannot be removed")
	}
	if req.GetMode() != azdext.RemoveLayerMode_REMOVE_LAYER_MODE_REQUIRE_EMPTY &&
		req.GetMode() != azdext.RemoveLayerMode_REMOVE_LAYER_MODE_CASCADE_SERVICES {
		return nil, status.Error(codes.InvalidArgument, "unknown layer removal mode")
	}

	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.reloadProjectForMutation(ctx)
	if err != nil {
		return nil, err
	}
	layers, err := s.layerImportManager().ListLayers(ctx, projectConfig)
	if err != nil {
		return nil, err
	}
	targetIndex := slices.IndexFunc(layers, func(layer *project.Layer) bool { return layer.Name == req.GetName() })
	if targetIndex < 0 {
		return nil, status.Errorf(codes.NotFound, "layer %q not found", req.GetName())
	}
	target := layers[targetIndex]

	var dependents []string
	for _, layer := range layers {
		if layer.Name != target.Name && slices.Contains(layer.DependsOn, target.Name) {
			dependents = append(dependents, layer.Name)
		}
	}
	if len(dependents) > 0 {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"layer %q is required by layers: %s",
			target.Name,
			strings.Join(dependents, ", "),
		)
	}
	if len(target.Services) > 0 && req.GetMode() == azdext.RemoveLayerMode_REMOVE_LAYER_MODE_REQUIRE_EMPTY {
		return nil, status.Errorf(codes.FailedPrecondition, "layer %q still contains services", target.Name)
	}

	removedServices := make([]string, 0, len(target.Services))
	if req.GetMode() == azdext.RemoveLayerMode_REMOVE_LAYER_MODE_CASCADE_SERVICES {
		for serviceName, service := range projectConfig.Services {
			if service.Layer != target.Name {
				continue
			}
			delete(projectConfig.Services, serviceName)
			removedServices = append(removedServices, serviceName)
		}
		slices.Sort(removedServices)
	}
	projectConfig.Infra.Layers = slices.DeleteFunc(projectConfig.Infra.Layers, func(layer provisioning.Options) bool {
		return layer.Name == target.Name
	})

	if err := project.Save(ctx, projectConfig, projectFilePath); err != nil {
		return nil, err
	}
	return &azdext.RemoveLayerResponse{RemovedServices: removedServices}, nil
}

// GetService gets a globally named service and optionally verifies its layer membership.
func (s *projectService) GetService(
	ctx context.Context,
	req *azdext.GetServiceRequest,
) (*azdext.GetServiceResponse, error) {
	if req == nil || req.GetServiceName() == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	services, err := s.layerImportManager().ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(services, func(service *project.ServiceConfig) bool {
		return service.Name == req.GetServiceName()
	})
	if index < 0 {
		return nil, status.Errorf(codes.NotFound, "service %q not found", req.GetServiceName())
	}
	service := services[index]
	if req.Layer != nil && service.Layer != req.GetLayer() {
		return nil, status.Errorf(
			codes.NotFound,
			"service %q does not belong to layer %q",
			service.Name,
			req.GetLayer(),
		)
	}

	var mapped *azdext.ServiceConfig
	if err := mapper.WithResolver(s.envResolver()).Convert(service, &mapped); err != nil {
		return nil, err
	}
	return &azdext.GetServiceResponse{Service: mapped}, nil
}

func (s *projectService) reloadProjectForMutation(
	ctx context.Context,
) (string, *project.ProjectConfig, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return "", nil, err
	}
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return "", nil, err
	}
	projectConfig, err := s.lazyProjectConfig.GetValue()
	return azdContext.ProjectPath(), projectConfig, err
}

func ensureLayerCreationIsSafe(ctx context.Context, projectFilePath, projectPath string) error {
	rawConfig, err := project.LoadConfig(ctx, projectFilePath)
	if err != nil {
		return err
	}
	if _, has := rawConfig.Get("infra"); has {
		return errors.New("cannot create layers while root infra configuration exists")
	}

	infraPath := filepath.Join(projectPath, "infra")
	directory, err := os.Open(infraPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting infrastructure directory: %w", err)
	}
	defer directory.Close()
	_, err = directory.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting infrastructure directory: %w", err)
	}
	return errors.New("cannot create layers while infrastructure files exist under infra")
}

func (s *projectService) layerImportManager() *project.ImportManager {
	if s.importManager != nil {
		return s.importManager
	}
	return project.NewImportManager(nil)
}

func (s *projectService) layerResponse(layer *project.Layer) (*azdext.LayerResponse, error) {
	mapped, err := s.layerToProto(layer)
	if err != nil {
		return nil, err
	}
	return &azdext.LayerResponse{Layer: mapped}, nil
}

func (s *projectService) layerToProto(layer *project.Layer) (*azdext.Layer, error) {
	result := &azdext.Layer{
		Name:      layer.Name,
		Services:  make(map[string]*azdext.ServiceConfig, len(layer.Services)),
		DependsOn: slices.Clone(layer.DependsOn),
		Inputs:    cloneStringMap(layer.Inputs),
		Outputs:   cloneStringMap(layer.Outputs),
		Implicit:  layer.Implicit,
	}
	if layer.Infra != nil {
		if err := mapper.Convert(*layer.Infra, &result.Infra); err != nil {
			return nil, err
		}
	}
	for _, service := range layer.Services {
		var mapped *azdext.ServiceConfig
		if err := mapper.WithResolver(s.envResolver()).Convert(service, &mapped); err != nil {
			return nil, err
		}
		result.Services[service.Name] = mapped
	}
	return result, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	return maps.Clone(values)
}
