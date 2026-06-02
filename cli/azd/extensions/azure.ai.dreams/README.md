# Dream Journal extension

`azure.ai.dreams` is an `azd` extension for saving and loading dreams from Azure Storage and optionally interpreting them with Azure AI.

## Commands

- `azd ai dream save --text "<dream>" [--title "<title>"]`
- `azd ai dream list`
- `azd ai dream load --id <dream-id>`
- `azd ai dream interpret --id <dream-id>`
- `azd ai dream interpret --text "<dream>"`

## Configuration

Set these values in your active azd environment (recommended) or shell:

- `DREAM_STORAGE_CONNECTION_STRING` (or `AZURE_STORAGE_CONNECTION_STRING`) - required
- `DREAM_STORAGE_CONTAINER` - optional (default: `dreams`)
- `DREAM_AI_ENDPOINT` - optional, required for `interpret`
- `DREAM_AI_KEY` - optional, required for `interpret`
- `DREAM_AI_DEPLOYMENT` - optional, required for `interpret`
- `DREAM_AI_API_VERSION` - optional (default: `2024-10-21`)
