# azure.yaml Schema

The `azure.yaml` file is the project configuration file for the Azure Developer CLI. It lives at the root of your project and declares services, infrastructure, and lifecycle hooks.

## Overview

```yaml
name: my-project
metadata:
  template: my-org/my-template
services:
  web:
    project: ./src/web
    language: js
    host: appservice
  api:
    project: ./src/api
    language: python
    host: containerapp
```

## Top-Level Properties

| Property | Type | Description |
|---|---|---|
| `name` | string | **Required.** Project name used for resource naming |
| `metadata` | object | Template metadata (origin template, version) |
| `resourceGroup` | string | Override the default resource group name |
| `services` | map | Service definitions keyed by service name |
| `pipeline` | object | CI/CD pipeline configuration |
| `hooks` | map | Project-level lifecycle hooks |
| `infra` | object | Infrastructure provider configuration |
| `state` | object | Remote state backend configuration |
| `resources` | map | Azure resource definitions |
| `requiredVersions` | object | Version constraints for azd and extensions |
| `platform` | object | Platform-specific configuration |
| `workflows` | object | Workflow configuration |
| `cloud` | object | Cloud environment configuration |

## Service Properties

| Property | Type | Description |
|---|---|---|
| `project` | string | Relative path to the service source directory |
| `language` | string | Service language (`dotnet`, `csharp`, `fsharp`, `py`, `js`, `ts`, `java`, `docker`, `custom`) |
| `host` | string | **Required.** Hosting target (`appservice`, `containerapp`, `function`, `staticwebapp`, `aks`, etc.) |
| `module` | string | Bicep module path for the service's infrastructure |
| `hooks` | map | Service-level lifecycle hooks |
| `docker` | object | Docker build configuration |
| `image` | string | Container image (alternative to `project` for pre-built images) |
| `dist` | string | Path to pre-built distribution directory |
| `resourceName` | string | Override the Azure resource name |
| `k8s` | object | Kubernetes-specific configuration |
| `config` | object | Service-specific configuration |
| `resourceGroup` | string | Override the resource group for this service |
| `apiVersion` | string | API version for the hosting target |
| `env` | map | Environment variables passed to the service |
| `uses` | list | Service dependencies |
| `remoteBuild` | boolean | Enable remote build for code-based Azure Functions |

### Docker Properties

| Property | Type | Description |
|---|---|---|
| `path` | string | Path to the Dockerfile |
| `context` | string | Docker build context path |
| `platform` | string | Container platform target |
| `target` | string | Dockerfile build target |
| `registry` | string | Destination container registry |
| `image` | string | Name applied to a built container image |
| `tag` | string | Tag applied to a built container image |
| `buildArgs` | list | Arguments passed to the container build |
| `network` | string | Networking mode for Dockerfile `RUN` instructions |
| `remoteBuild` | boolean | Prefer building and pushing with Azure Container Registry; fall back locally only when ACR refuses scheduling with `TasksOperationsNotAllowed` |
| `imagePassthrough` | boolean | Reuse an existing remote service `image` without building or publishing it; `azd deploy --from-package` can override the image for one deployment |

If ACR refuses scheduling with `TasksOperationsNotAllowed`, azd warns and falls back automatically, including in non-interactive runs. Docker or Podman must be installed and running. Fallback publishes a supplied local package or builds and pushes from source.

Other errors, including build failures, cancellation, and failures reading remote logs or status, do not trigger fallback. If fallback also fails, azd preserves both errors. Set `docker.remoteBuild: false` to build locally.

If the local runtime is unavailable, the error explains both the ACR refusal and the failed fallback. Check that Docker or Podman is running and accessible before retrying; the underlying error includes connection or permission details.

`docker.imagePassthrough` declares that azd does not own the container image lifecycle. It requires the service-level
`image` property to contain a fully qualified remote image and cannot be combined with `docker.remoteBuild`. During package, publish, and deploy operations, azd
uses the configured image as the existing remote image without building, pulling, tagging, copying, or publishing it:

```yaml
services:
  api:
    host: containerapp
    image: registry.example.com/apps/api:1.0
    docker:
      imagePassthrough: true
```

The service `image` is the default. A fully qualified remote image supplied to `azd deploy --from-package` overrides it
for that deployment and is also passed through unchanged:

```bash
azd deploy api --from-package other-registry.example.com/apps/api:2.0
```

Passthrough overrides do not support local image names, archives, or directories. The `--from-package` override above
applies only to `azd deploy`; `azd publish --from-package` and `azd publish --to` are not supported for passthrough
services. Running `azd publish` without either flag reuses the configured remote image and does not publish it.

azd does not sign in to the source registry or verify access to it in this mode. The destination platform must already
have permission to pull the image through its managed identity or registry credentials.

When `imagePassthrough` is omitted or `false`, an external service image can still be pulled and copied into the
configured destination registry.

## Infrastructure Layer Inputs and Outputs

Each entry under `infra.layers[]` or `layers[].infra[]` can define provider-local
inputs and map provider outputs to the shared project environment. These properties
are not supported on root `infra`.

| Property | Type | Description |
|---|---|---|
| `env` | map of strings | Provider-local input names and literal values or environment-substitution expressions. Supported by Bicep and Terraform |
| `outputs` | map of strings | `PROVIDER_OUTPUT: PROJECT_VARIABLE` mappings. Supported by all providers, including custom and extension providers |

```yaml
infra:
  layers:
    - name: producer
      path: infra/producer
      provider: bicep
      outputs:
        PROVIDER_ENDPOINT: PROJECT_ENDPOINT
    - name: consumer
      path: infra/consumer
      provider: terraform
      env:
        PROVIDER_INPUT: ${PROJECT_ENDPOINT}
        PROVIDER_URL: https://${PROJECT_HOST}/api
        RETRY_COUNT: "3"
        OPTIONAL_INPUT: ${OPTIONAL_PROJECT_INPUT:-fallback}
        EMPTY_INPUT: ""
```

### Provider-local inputs

The `env` key is the name in the **[provider view](../concepts/glossary.md#provider-view)**.
For parameter inputs, this is the environment variable referenced by the parameter
file, not necessarily the IaC parameter name. Its value is a string: a literal,
an environment-substitution expression, or a combination of both.

azd evaluates these expressions once when it initializes the provider, using the
shared **[project view](../concepts/glossary.md#project-view)** with process-environment
fallback. An explicitly empty project value overrides process fallback. Missing
or empty references expand to empty unless the expression supplies a default,
such as `${PROJECT_INPUT:-fallback}`.

All entries use the same source view. They do not read each other's expanded
values: with `A: ${B}` and `B: ${C}`, `A` reads shared `B`, not the local value
derived from `C`. Multiple entries can read the same source variable.

Only keys explicitly listed in `env` are local. Environment writes and deletes for
these keys stay in that provider instance and do not change the shared project
environment. Other variable reads, writes, deletes, and provider-specific
configuration retain their existing behavior. An explicitly empty local value
also overrides fallback.

Custom and extension providers reject nonempty `env` maps. An empty `env: {}` is
allowed, as is an `outputs` map without `env`. azd also checks the effective
provider at runtime when it is inherited or inferred.

### Output names

The `outputs` map only renames provider outputs. It does not define provider inputs
or redirect general environment writes. The mapping applies once to outputs from
deployment, state reads, and `azd env refresh`; planned output metadata; lifecycle
events; and keys invalidated during destroy. Unmapped outputs retain their names.
With `A: B` and `B: C`, output `A` becomes project variable `B`, not `C`.

Service environment-update events use these project output names, including
the .NET user-secrets integration, which converts `__` in names to `:`.

A mapped output can collide with an unmapped output. Since provider outputs are
not all declared in `azure.yaml`, azd checks the returned names for collisions
before writing deployment outputs.

Post-deploy `azd.operations.yaml` processing uses a temporary component view with
the provider's current local input values and raw returned output names. Returned
outputs take precedence in this view only; they do not replace the provider's
original inputs. This lets a reusable operations file keep referring to its local
output names even when the project publishes different names. Local input values
and raw output names are not persisted by operations processing. Hooks continue to
use the project environment.

### Names and execution context

`env` keys and both sides of `outputs` must match `^[A-Za-z_][A-Za-z0-9_]*$`.
Within each infrastructure entry, azd checks `env` keys, `outputs` keys, and
`outputs` destinations for case-insensitive uniqueness so the configuration
works consistently on Windows. Repeated source variables in `env` expressions
are allowed.

The following azd execution-context variables are shared across infrastructure
entries. They cannot be `env` keys or appear on either side of `outputs`:

- `AZURE_ENV_NAME`
- `AZURE_SUBSCRIPTION_ID`
- `AZURE_TENANT_ID`
- `AZURE_LOCATION`
- `AZURE_RESOURCE_GROUP`
- `AZURE_PRINCIPAL_ID`
- `AZURE_PRINCIPAL_TYPE`
- Any variable whose name starts with `AZD_`, matched case-insensitively

`env` expressions may read these shared variables, for example
`COMPONENT_REGION: ${AZURE_LOCATION}`. Use local keys for component inputs, not
for environment identity, deployment scope, authentication identity, or process controls.

### Dependencies and pipelines

In the example, the producer's `PROVIDER_ENDPOINT` output is stored as
`PROJECT_ENDPOINT`, and the consumer reads it as `PROVIDER_INPUT`. Static dependency
analysis follows `env` expression sources for Bicep or Terraform consumers of known
Bicep outputs, using the mapped project output names. A local `env` key shadows
the shared variable with that name; analysis follows its expression without
recursively following other local entries. Literals introduce no source dependency.

Terraform and custom provider output names are not discovered statically. Use
`dependsOn: [producer]` when a consumer depends on these outputs, or on values
written by hooks.

Pipeline configuration exports the source project variables and secrets referenced
by input expressions, not the derived strings or provider-local names. Literal
values require no exports. Outputs from earlier layers are tracked by their project
names when planning later layers and are not exported as CI inputs because provisioning
creates them. When the provider supplies no sensitivity metadata for a referenced
source, azd conservatively exports it as a secret. Source names are deduplicated;
if any reference marks a source secret, secret classification wins.

CI export preserves explicitly empty source values instead of replacing them
with process-environment values. Process fallback applies only when the project
source is absent, so expressions with defaults behave the same locally and in CI.

If a parameter still requires a locally prompted or generated value despite its `env` inputs,
pipeline configuration stops with an error rather than discarding that value.
Set the source project variables or correct the infrastructure entry's `env`
values before configuring the pipeline. Unrelated parameters keep their existing
prompt-to-CI behavior.

Local inputs do not bypass the environment filter for dynamic loader variables.
Names in the `LD_` and `DYLD_` namespaces are excluded from `Dotenv()` and
subprocess environments, including when introduced through `env`.

## Hooks

Hooks run user-defined scripts at lifecycle points:

```yaml
hooks:
  preprovision:
    kind: sh
    run: ./scripts/setup.sh
  postdeploy:
    kind: sh
    run: ./scripts/smoke-test.sh
```

Available hook points (each supports `pre` and `post` prefixes):

- **Command hooks (project-level):** `build`, `deploy`, `down`, `package`, `provision`, `publish`, `restore`, `up`
- **Service lifecycle hooks (service-level):** `restore`, `build`, `package`, `publish`, `deploy`

For example, `preprovision` runs before provisioning, `postdeploy` runs after deployment. Service-level hooks are defined under a service's `hooks` section in `azure.yaml` and apply only to that service.

## JSON Schema

The full JSON schema for `azure.yaml` is maintained in the [schemas/](../../schemas/) directory and published for editor validation.

## Host-Specific Notes

### App Service (`host: appservice`)

App Service supports two deployment modes:

- **Zip deploy** (default): When `language` is set to a non-Docker language (e.g., `python`, `js`, `dotnet`), azd builds the code, creates a zip archive, and deploys it via the Kudu zip deploy API.
- **Container deploy**: When `language: docker` is set, azd builds the container image, pushes it to ACR, and updates the site's `linuxFxVersion`. Currently Linux App Service only. Your infrastructure (bicep/terraform) must configure ACR access (e.g., managed identity ACR pull, identity assignment) before deploying. azd only updates the image reference at deploy time.

**Note**: Container deployment supports both `language: docker` (with a Dockerfile) and polyglot containerization (e.g., `language: python` with `docker.path` pointing to a Dockerfile). You can also use a pre-built `image:` without local source.

Example container deployment:

```yaml
services:
  web:
    project: ./src/web
    language: docker
    host: appservice
    docker:
      path: ./Dockerfile
```

### Function App (`host: function`)

Function Apps support code and container deployment:

- **Zip deploy** (default): When no container configuration is present, azd builds the Function project, creates a zip archive, and deploys it through the Function App deployment API. The top-level `remoteBuild` property applies only to this mode.
- **Container deploy**: Configure `language: docker`, set `docker.path` for a non-Docker language, or provide a pre-built `image`. azd builds or resolves the image, publishes it to ACR when needed, and updates the Function App's `linuxFxVersion`.

Container-based Function infrastructure must configure a Linux Function App, an initial `DOCKER|` image reference, and registry pull access before deployment. These settings remain the responsibility of Bicep or Terraform. If the provisioned Function App and the service disagree about the deployment mode, azd fails before attempting an incompatible upload and identifies the configuration mismatch.

Unlike App Service, Function Apps always deploy to the main site. Deployment slots are not part of the Function App workflow, so `AZD_DEPLOY_{SERVICE}_SLOT_NAME` has no effect and azd never prompts for a slot.

Example TypeScript container deployment:

```yaml
services:
  function:
    project: ./src/function
    language: ts
    host: function
    docker:
      path: ./Dockerfile
```

## See Also

- [azure.yaml JSON Schema](../../schemas/) — Machine-readable schema definition
- [Feature Status](feature-status.md) — Which languages and hosts are stable/beta/alpha
