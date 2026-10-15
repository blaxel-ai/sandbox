# Contributing

## Setup

```bash
make dependencies   # air, swag, yq
make api            # sandbox-api on :8080 with hot reload
```

Or run it in Docker with `docker-compose up dev`.

## Tasks

Run from the repo root:

- `make test` - Unit tests (`go test ./...` in `sandbox-api`)
- `make integration-test` - Integration tests against a running API (see [integration-tests](sandbox-api/integration-tests/README.md))
- `make identity-test` - Same suite with the workload running as an unprivileged user (needs root)
- `make reference` - Regenerate `sandbox-api/docs/openapi.yml` after changing a handler annotation
- `make benchmark` - Go benchmarks

Run `go vet ./...` in `sandbox-api` before pushing: CI runs it with the tests.

## Hub templates

Each template lives in `hub/<name>/` with a `Dockerfile` and a `template.json`:

```json
{
  "name": "template-name",
  "displayName": "Template Name",
  "categories": ["web"],
  "description": "One or two sentences.",
  "longDescription": "Longer description.",
  "url": "https://link-to-the-technology",
  "icon": "https://link-to-an-icon.png",
  "memory": 2048,
  "ports": [
    { "name": "sandbox-api", "target": 8080, "protocol": "HTTP" }
  ],
  "enterprise": false,
  "coming_soon": false
}
```

- Start from `docker.io/baseten/sandbox` and keep `sandbox-api` on port 8080.
- Web app templates put the project in `/app`. Generic and agent templates work in `/workspace`.
- `iconLight` and `iconDark` are optional; see [Configuration](README.md#configuration) for the fallback rules.
- Add the template to `docker-compose.yaml` and check it builds and serves its ports locally.

CI ([build.yaml](.github/workflows/build.yaml)) pushes `sandbox-api` to `docker.io/baseten/sandbox`, then builds every changed `hub/<name>` template from its Dockerfile on the hub builder and publishes it. Only `docker.io/baseten/sandbox` is public; template images live in the hub.

## Releasing

Pushes to `main` that touch `sandbox-api/` publish a prerelease named after the branch. For a versioned release, tag `main`:

```bash
make tag 1.2.3   # pushes v1.2.3
```

The [release workflow](.github/workflows/release.yaml) builds the `sandbox-api` binaries for Linux and macOS and attaches them to the GitHub release.
