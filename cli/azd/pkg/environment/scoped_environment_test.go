// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentImplementsScopedEnvironment(t *testing.T) {
	t.Parallel()

	backing := New("test")
	var env ScopedEnvironment = backing
	require.Same(t, backing, env.BackingEnv())
	require.Same(t, backing.Config, env.GetConfig())
	env.SetSubscriptionId("subscription")
	env.SetLocation("location")
	env.DotenvSet(TenantIdEnvVarName, "tenant")
	env.SetServiceProperty("api", "ENDPOINT", "endpoint")

	require.Equal(t, "subscription", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
	require.Equal(t, "tenant", env.GetTenantId())
	require.Equal(t, "endpoint", env.GetServiceProperty("api", "ENDPOINT"))
	require.Equal(t, backing.Dotenv(), env.Dotenv())
}

func TestInputEnvironmentLocalMutations(t *testing.T) {
	t.Setenv("LOCAL_INPUT", "ambient")
	backing := NewWithValues("test", map[string]string{
		"LOCAL_INPUT": "project-original",
		"SOURCE":      "source",
		"UNCHANGED":   "shared",
	})
	inputs := map[string]string{"LOCAL_INPUT": "resolved", "EMPTY": ""}
	env := NewInputEnvironment(backing, inputs)
	inputs["LOCAL_INPUT"] = "changed-after-construction"

	require.Equal(t, "resolved", env.Getenv("LOCAL_INPUT"))
	require.Equal(t, "shared", env.Getenv("UNCHANGED"))
	require.Same(t, backing, env.BackingEnv())
	require.Same(t, backing.Config, env.GetConfig())
	value, present := env.LookupEnv("EMPTY")
	require.True(t, present)
	require.Empty(t, value)
	require.Contains(t, env.Environ(), "EMPTY=")

	env.DotenvSet("LOCAL_INPUT", "local-write")
	require.Equal(t, "local-write", env.Getenv("LOCAL_INPUT"))
	require.Equal(t, "local-write", env.Dotenv()["LOCAL_INPUT"])
	require.Equal(t, "project-original", backing.Getenv("LOCAL_INPUT"))
	require.Equal(t, "source", backing.Getenv("SOURCE"))
	env.DotenvDelete("LOCAL_INPUT")
	_, present = env.LookupEnv("LOCAL_INPUT")
	require.False(t, present, "deletion must not reveal the project or process value")
	require.NotContains(t, env.Dotenv(), "LOCAL_INPUT")
	_, present = lookupProcessEnv(env.ProcessEnviron([]string{"LOCAL_INPUT=ambient"}), "LOCAL_INPUT")
	require.False(t, present)
	env.DotenvSet("LOCAL_INPUT", "restored")
	require.Equal(t, "restored", env.Getenv("LOCAL_INPUT"))

	env.DotenvSet("UNCHANGED", "persisted-write")
	require.Equal(t, "persisted-write", backing.Getenv("UNCHANGED"))
	env.DotenvDelete("UNCHANGED")
	require.NotContains(t, backing.Dotenv(), "UNCHANGED")
	require.NoError(t, env.GetConfig().Set("infra.parameters.value", "shared-config"))
	value, present = backing.Config.GetString("infra.parameters.value")
	require.True(t, present)
	require.Equal(t, "shared-config", value)
}

func TestInputEnvironmentKeepsResolvedSnapshot(t *testing.T) {
	t.Parallel()

	backing := NewWithValues("test", map[string]string{"SOURCE": "original"})
	env := NewInputEnvironment(backing, map[string]string{"LOCAL": "original"})
	backing.DotenvSet("SOURCE", "changed")

	require.Equal(t, "original", env.Getenv("LOCAL"))
	require.Equal(t, "changed", env.Getenv("SOURCE"))
}

func TestInputEnvironmentProcessEnviron(t *testing.T) {
	t.Parallel()

	base := []string{"LOCAL=ambient", "EMPTY=ambient", "KEEP=preserved"}
	before := slices.Clone(base)
	backing := NewWithValues("test", map[string]string{"LOCAL": "project", "OTHER": "other"})
	env := NewInputEnvironment(backing, map[string]string{"LOCAL": "resolved", "EMPTY": ""})

	values := env.ProcessEnviron(base)
	value, present := lookupProcessEnv(values, "LOCAL")
	require.True(t, present)
	require.Equal(t, "resolved", value)
	value, present = lookupProcessEnv(values, "EMPTY")
	require.True(t, present)
	require.Empty(t, value)
	require.NotContains(t, values, "LOCAL=ambient")
	require.NotContains(t, values, "LOCAL=project")
	require.Contains(t, values, "KEEP=preserved")
	require.Contains(t, values, "OTHER=other")
	require.Equal(t, before, base)
	require.Equal(t, "project", backing.Getenv("LOCAL"))
}

func TestInputEnvironmentProcessNameMatching(t *testing.T) {
	t.Parallel()

	env := NewInputEnvironment(New("test"), map[string]string{"LOCAL": "resolved"})
	values := env.ProcessEnviron([]string{"local=ambient"})
	if runtime.GOOS == "windows" {
		require.NotContains(t, values, "local=ambient")
	} else {
		require.Contains(t, values, "local=ambient")
	}
	require.Contains(t, values, "LOCAL=resolved")
}

func TestInputEnvironmentFiltersLoaderControls(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"LD_PRELOAD", "ld_library_path", "DYLD_INSERT_LIBRARIES"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			env := NewInputEnvironment(New("test"), map[string]string{key: "not-for-processes"})
			require.Equal(t, "not-for-processes", env.Getenv(key))
			require.NotContains(t, env.Dotenv(), key)
			require.NotContains(t, env.Environ(), key+"=not-for-processes")
			require.NotContains(t, env.ProcessEnviron(nil), key+"=not-for-processes")
		})
	}
}

func TestInputEnvironmentPreservesSharedExecutionContext(t *testing.T) {
	t.Parallel()

	backing := NewWithValues("test", map[string]string{
		SubscriptionIdEnvVarName: "subscription", LocationEnvVarName: "location",
		TenantIdEnvVarName: "tenant",
	})
	env := NewInputEnvironment(backing, map[string]string{"LOCAL": "input"})
	require.Equal(t, "subscription", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
	require.Equal(t, "tenant", env.GetTenantId())
	env.SetLocation("new-location")
	require.Equal(t, "new-location", backing.GetLocation())
}

func TestInputEnvironmentServicePropertiesStayLocal(t *testing.T) {
	t.Parallel()

	backing := New("test")
	env := NewInputEnvironment(backing, map[string]string{"SERVICE_API_ENDPOINT": "initial"})
	env.SetServiceProperty("api", "ENDPOINT", "updated")
	require.Equal(t, "updated", env.GetServiceProperty("api", "ENDPOINT"))
	require.Empty(t, backing.GetServiceProperty("api", "ENDPOINT"))
}

func TestInputEnvironmentConcurrentAccess(t *testing.T) {
	t.Parallel()

	env := NewInputEnvironment(New("test"), map[string]string{"LOCAL": "input"})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				env.DotenvSet("LOCAL", "updated")
				env.Getenv("LOCAL")
				env.Dotenv()
				env.ProcessEnviron(nil)
				env.DotenvDelete("LOCAL")
			}
		})
	}
	wg.Wait()
}

func TestCompat_NoInputsDoesNotWrapEnvironment(t *testing.T) {
	t.Parallel()

	backing := New("test")
	require.Same(t, backing, NewInputEnvironment(backing, nil))
	require.Same(t, backing, NewInputEnvironment(backing, map[string]string{}))
}

func TestEnvironmentProcessEnviron(t *testing.T) {
	t.Parallel()

	env := NewWithValues("test", map[string]string{"KEY": "dotenv", "LD_PRELOAD": "blocked"})
	base := []string{"KEY=inherited", "KEEP=preserved"}
	before := slices.Clone(base)
	values := env.ProcessEnviron(base)
	value, present := lookupProcessEnv(values, "KEY")
	require.True(t, present)
	require.Equal(t, "dotenv", value)
	require.Contains(t, values, "KEEP=preserved")
	require.NotContains(t, values, "LD_PRELOAD=blocked")
	require.Equal(t, before, base)
	require.NotNil(t, New("test").ProcessEnviron(nil))
}

func lookupProcessEnv(values []string, key string) (string, bool) {
	for _, entry := range slices.Backward(values) {
		name, value, found := strings.Cut(entry, "=")
		if found && processEnvNameEqual(name, key) {
			return value, true
		}
	}
	return "", false
}
