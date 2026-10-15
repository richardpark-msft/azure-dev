// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// ScopedEnvironment exposes a component's variable view to provisioning providers.
// Environment identity, deployment scope, principal identity, and azd control variables
// remain part of the shared backing environment.
type ScopedEnvironment interface {
	// BackingEnv returns the underlying environment state.
	// It may be a layer-local snapshot rather than the shared environment.
	BackingEnv() *Environment

	// Name returns the backing environment name, falling back to AZURE_ENV_NAME when it is empty.
	Name() string
	// Getenv returns a provider-scoped value, preferring dotenv over the process environment.
	Getenv(key string) string
	// LookupEnv is like Getenv, but distinguishes an unset value from an explicitly empty value.
	LookupEnv(key string) (string, bool)
	// Dotenv returns a copy of the provider-visible dotenv values without process environment fallbacks.
	Dotenv() map[string]string
	// DotenvSet updates an explicit input locally; other keys update the backing dotenv state.
	DotenvSet(key, value string)
	// DotenvDelete removes an explicit input locally; other keys are removed from the backing dotenv state.
	DotenvDelete(key string)
	// Environ formats only the provider-visible dotenv values as KEY=VALUE entries.
	Environ() []string
	// ProcessEnviron builds a complete environment for a child process.
	// base is typically os.Environ(), which supplies the real process environment.
	//
	//	childEnv := env.ProcessEnviron(os.Environ())
	//	runArgs := exec.NewRunArgs("tool").WithExactEnv(childEnv)
	ProcessEnviron(base []string) []string
	GetSubscriptionId() string
	SetSubscriptionId(id string)
	GetTenantId() string
	GetLocation() string
	SetLocation(location string)
	GetServiceProperty(serviceName, propertyName string) string
	SetServiceProperty(serviceName, propertyName, value string)
	GetConfig() config.Config
}

var _ ScopedEnvironment = (*Environment)(nil)

// BackingEnv returns this environment.
func (e *Environment) BackingEnv() *Environment {
	return e
}

// GetConfig returns the environment configuration.
func (e *Environment) GetConfig() config.Config {
	return e.Config
}

// ProcessEnviron returns inherited values followed by dotenv values, which take precedence.
func (e *Environment) ProcessEnviron(base []string) []string {
	dotenv := e.Environ()
	values := make([]string, 0, len(base)+len(dotenv))
	values = append(values, base...)
	return append(values, dotenv...)
}

type inputEnvironment struct {
	ScopedEnvironment
	keys map[string]struct{}
	// keys is immutable; mu protects the provider-local values against concurrent access.
	mu     sync.RWMutex
	values map[string]string
}

// NewInputEnvironment overlays resolved input values without persisting them to the backing environment.
// Writes and deletes to explicit input keys remain local. All other keys retain their backing behavior.
func NewInputEnvironment(
	env ScopedEnvironment,
	values map[string]string,
) ScopedEnvironment {
	if len(values) == 0 {
		return env
	}

	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}

	return &inputEnvironment{
		ScopedEnvironment: env,
		keys:              keys,
		values:            maps.Clone(values),
	}
}

func (e *inputEnvironment) Getenv(key string) string {
	value, _ := e.LookupEnv(key)
	return value
}

func (e *inputEnvironment) LookupEnv(key string) (string, bool) {
	if _, overridden := e.keys[key]; overridden {
		e.mu.RLock()
		defer e.mu.RUnlock()
		value, found := e.values[key]
		return value, found
	}
	return e.ScopedEnvironment.LookupEnv(key)
}

func (e *inputEnvironment) Dotenv() map[string]string {
	values := e.ScopedEnvironment.Dotenv()
	e.mu.RLock()
	defer e.mu.RUnlock()

	for key := range e.keys {
		delete(values, key)
		if value, found := e.values[key]; found && !isLoaderControlKey(key) {
			values[key] = value
		}
	}

	return values
}

func (e *inputEnvironment) DotenvSet(key string, value string) {
	if _, overridden := e.keys[key]; overridden {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.values[key] = value
		return
	}
	e.ScopedEnvironment.DotenvSet(key, value)
}

func (e *inputEnvironment) DotenvDelete(key string) {
	if _, overridden := e.keys[key]; overridden {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.values, key)
		return
	}
	e.ScopedEnvironment.DotenvDelete(key)
}

func (e *inputEnvironment) Environ() []string {
	values := e.Dotenv()
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return env
}

// ProcessEnviron overlays inputs on the complete child environment.
// Deleted inputs remain absent even when the inherited environment contains the same key.
func (e *inputEnvironment) ProcessEnviron(base []string) []string {
	resolvedBase := e.ScopedEnvironment.ProcessEnviron(base)
	inputNames := slices.Sorted(maps.Keys(e.keys))
	values := slices.DeleteFunc(slices.Clone(resolvedBase), func(entry string) bool {
		name, _, found := strings.Cut(entry, "=")
		return found && slices.ContainsFunc(inputNames, func(inputName string) bool {
			return processEnvNameEqual(name, inputName)
		})
	})

	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, inputName := range inputNames {
		if isLoaderControlKey(inputName) {
			continue
		}
		if value, found := e.values[inputName]; found {
			values = append(values, inputName+"="+value)
		}
	}
	return values
}

func processEnvNameEqual(name, key string) bool {
	// Windows treats process environment names as case-insensitive, unlike Unix.
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, key)
	}
	return name == key
}

func (e *inputEnvironment) GetServiceProperty(serviceName, propertyName string) string {
	return e.Getenv(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName))
}

func (e *inputEnvironment) SetServiceProperty(serviceName, propertyName, value string) {
	e.DotenvSet(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName), value)
}
