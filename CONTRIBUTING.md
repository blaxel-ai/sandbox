# Contributing to Sandbox Hub

Thank you for your interest in contributing to Sandbox Hub! This document provides guidelines and instructions for contributing to the project.

## How Can I Contribute?

### Reporting Bugs

- Check if the bug has already been reported in the Issues section
- Use the bug report template when creating a new issue
- Include detailed steps to reproduce the bug
- Provide information about your environment (OS, Docker version, etc.)
- If possible, include screenshots or error logs

### Suggesting Enhancements

- Check if the enhancement has already been suggested in the Issues section
- Use the feature request template when creating a new issue
- Clearly describe the feature and its benefits
- If possible, outline how the feature might be implemented

### Creating New Templates

1. **Design Your Template**:
   - Identify a use case or development environment not covered by existing templates
   - Define the required tools, languages, and dependencies

2. **Implement Your Template**:
   - Create a new directory in the `hub` folder with a descriptive name
   - Add a `Dockerfile` that sets up the required environment
   - Create a comprehensive `template.json` file following the established format

3. **Test Your Template**:
   - Build and test your template locally
   - Verify all specified ports work correctly
   - Test with different configuration options

4. **Submit Your Template**:
   - Create a pull request with your new template
   - Include documentation explaining the template's purpose and usage

### Template JSON Requirements

Every `template.json` file should include:

```json
{
  "name": "template-name",         // Required: Short name, no spaces
  "displayName": "Template Name",  // Required: User-friendly name
  "categories": [],                // Required: Array of relevant categories
  "description": "",               // Required: Short description (1-2 sentences)
  "longDescription": "",           // Required: Detailed description
  "url": "",                       // Required: URL to learn more about the technology
  "icon": "",                      // Required: URL to an icon image
  "memory": 2048,                  // Required: Memory allocation in MB
  "ports": [                       // Required: Array of exposed ports
    {
      "name": "port-name",
      "target": 8080,              // Port number
      "protocol": "HTTP"           // Protocol (HTTP, HTTPS, TCP, UDP, tls)
    }
  ],
  "enterprise": false,             // Optional: Enterprise-only feature flag
  "coming_soon": false             // Optional: Coming soon flag
}
```

## Pull Request Process

1. Fork the repository and create your branch from `main`
2. If you've added code that should be tested, add tests
3. Ensure your code follows the project's style guidelines
4. Update the documentation if necessary
5. Make sure your code passes all tests
6. Issue a pull request with a comprehensive description of changes

## Development Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/blaxel-ai/sandbox.git
   cd sandbox
   ```

2. Start the development environment:
   ```bash
   docker-compose up -d
   ```

## How Hub Images Are Built

`.github/workflows/build.yaml` builds each changed `hub/<name>` image in two steps:

1. `build-ghcr-hub` pushes the Docker image to `ghcr.io/blaxel-ai/sandbox-<name>:<tag>`.
2. `build-blaxel-hub` runs `bl push --image <ghcr ref>` into the `blaxel` workspace, authenticated with the `BL_BUILD_API_KEY` environment secret, then publishes the template to the store. The in-sandbox builder converts the image, the build logs stream into the job, the image is registered as `blaxel/<name>` when it succeeds, and the job fails when the build fails. Only amd64 images go through this step.
   The same images are also pushed to the `baseten` workspace (`BL_BASETEN_BUILD_API_KEY`), without store publication. That build is skipped in an environment where the key is not set.

### Testing without touching production

Push to `develop`: it builds against the dev control plane only. To build without publishing to the dev store, run `bl push` locally under another name:

```bash
BL_API_KEY=<dev key> BL_ENV=dev bl push -y -w blaxel -t sandbox -n <name>-canary \
  --image ghcr.io/blaxel-ai/sandbox-<name>:<tag>
```

### When the builder is broken

Hub images are built by the `blaxel/inf/builder` image, which is built in `blaxel-ai/executionplane-services` by AWS Batch, never by itself. If it is broken:

1. **Rebuild the builder**: run the `executionplane-services` build workflow manually with `service=builder` on the branch of the target environment (`develop` for dev, `main` for prod). It runs on AWS Batch, so it does not need a working builder. The builder embeds `sandbox-api` from `ghcr.io/blaxel-ai/sandbox:latest`: if a `sandbox-api` release broke it, fix or roll back that release first.
2. **Roll back right away**: point the control plane at a known good builder with `BUILD_SANDBOX_IMAGE=blaxel/inf/builder:<good tag>`.
3. Re-run this workflow manually for the images that failed.

## Styleguides

### Git Commit Messages

- Use the present tense ("Add feature" not "Added feature")
- Use the imperative mood ("Move cursor to..." not "Moves cursor to...")
- Limit the first line to 72 characters or less
- Reference issues and pull requests after the first line

### Dockerfile Guidelines

- Use official base images whenever possible
- Minimize the number of layers
- Avoid installing unnecessary packages
- Group related commands to reduce layers
- Use multi-stage builds when appropriate
- Include proper comments and labels

## Additional Notes

### Issue and Pull Request Labels

| Label Name | Description |
|------------|-------------|
| `bug` | Confirmed bugs or reports likely to be bugs |
| `enhancement` | Feature requests |
| `documentation` | Documentation improvements |
| `good-first-issue` | Good for newcomers |
| `help-wanted` | Extra attention is needed |
| `template` | Related to template development |

---

Thank you for contributing to Sandbox Hub!