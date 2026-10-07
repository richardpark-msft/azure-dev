// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_DefaultPlatform_TerraformCliIsScoped(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	container := mockContext.Container
	ioc.RegisterInstance[exec.CommandRunner](container, mockContext.CommandRunner)
	require.NoError(t, NewDefaultPlatform().ConfigureContainer(container))
	firstScope, err := container.NewScope()
	require.NoError(t, err)
	secondScope, err := container.NewScope()
	require.NoError(t, err)
	var firstCli, secondCli *terraform.Cli
	require.NoError(t, firstScope.Resolve(&firstCli))
	require.NoError(t, secondScope.Resolve(&secondCli))
	require.NotSame(t, firstCli, secondCli)
}

func Test_DefaultPlatform_IsEnabled(t *testing.T) {
	t.Run("Enabled", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		require.True(t, defaultPlatform.IsEnabled())
	})
}

func Test_DefaultPlatform_ConfigureContainer(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		container := ioc.NewNestedContainer(nil)
		err := defaultPlatform.ConfigureContainer(container)
		require.NoError(t, err)

		var provisionResolver provisioning.DefaultProviderResolver
		err = container.Resolve(&provisionResolver)
		require.NoError(t, err)
		require.NotNil(t, provisionResolver)

		expected := provisioning.Bicep
		actual, err := provisionResolver()
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}
