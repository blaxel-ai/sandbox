---
name: update-api-reference
description: Regenerate the OpenAPI reference documentation after adding, modifying, or removing API endpoints in sandbox-api. Run this whenever handler signatures, route paths, or swag annotations change.
---

# Update the API Reference (OpenAPI Docs)

The OpenAPI spec is generated from [swaggo](https://github.com/swaggo/swag) annotations in the Go handler code. After changing any endpoint, regenerate the docs.

## Run the Full Reference Update

```bash
make reference
```

This single command:
1. Runs `swag init` to parse Go annotations → generates `sandbox-api/docs/swagger.yaml` and `docs.go`
2. Fixes type name references (replaces `filesystem.Directory` with `Directory`)
3. Converts Swagger 2.0 → OpenAPI 3.0 via `swagger2openapi`
4. Adds Bearer auth security scheme to all endpoints
5. Runs `fixopenapi.sh` for additional patches

The output file is `sandbox-api/docs/openapi.yml`.

---

## How Swag Annotations Work

Annotations are Go comments above handler functions. Example:

```go
// HandleGetFile retrieves a file from the sandbox filesystem.
//
// @Summary      Get file or directory
// @Description  Returns file content or directory listing
// @Tags         filesystem
// @Produce      application/octet-stream
// @Param        path  path  string  true  "File path"
// @Success      200   {string}  string  "File content"
// @Failure      404   {object}  ErrorResponse
// @Router       /filesystem/{path} [get]
func (h *FileSystemHandler) HandleGetFile(c *gin.Context) {
```

Key annotation fields:
- `@Summary` — short description (shown in API reference)
- `@Description` — longer explanation
- `@Tags` — group endpoints in the UI (filesystem, process, network, codegen)
- `@Param` — parameter: `name location type required "description"`
  - locations: `path`, `query`, `body`, `header`
- `@Success` / `@Failure` — response codes with type and description
- `@Router` — path and HTTP method `[get|post|put|delete]`
- `@Accept` / `@Produce` — request/response content types

---

## Where the Docs Live

| File | Purpose |
|------|---------|
| `sandbox-api/docs/openapi.yml` | Final OpenAPI 3.0 spec (commit this) |
| `sandbox-api/docs/docs.go` | Auto-generated Go embed of the spec |
| `sandbox-api/docs/fixopenapi.sh` | Post-processing patches |

---

## After Adding a New Endpoint

1. Add swag annotations to your handler function
2. Register the route in `sandbox-api/src/api/router.go`
3. Run `make reference`
4. Review the diff in `sandbox-api/docs/openapi.yml` to confirm your endpoint appears correctly
5. Commit both the handler changes and the updated `openapi.yml` and `docs.go`

---

## Verify the Generated Docs

Open the Swagger UI at `http://localhost:8080/swagger/index.html` while the dev server is running. It reads `docs.go` which is embedded at build time.

---

## Troubleshooting

- **`swag: command not found`**: Run `make dependencies` first
- **Missing endpoint in output**: Check that your handler has a `@Router` annotation and the function is exported
- **Type not found**: Ensure the struct is in the same package or imported and annotated with `@Description`
- **`swagger2openapi` not found**: Run `npm install -g swagger2openapi` or `npx swagger2openapi` (the Makefile uses `npx`)
