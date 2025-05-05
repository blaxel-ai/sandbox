<p align="center">
  <img src="https://blaxel.ai/logo.png" alt="Blaxel"/>
</p>

# Sandboxes

## Overview

Sandbox Hub is a collection of development environment templates for creating secure, isolated micro VM environments for various application types. Each template provides a pre-configured environment with all necessary tools and dependencies for specific development scenarios.

## Table of Contents

- [Templates](#templates)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Contributing](#contributing)
- [License](#license)
- [Support](#support)

## Templates

The hub directory contains the following templates:

### Base
A minimal micro VM environment with basic system utilities and networking capabilities. Provides a lightweight, secure foundation for running basic applications and services, featuring a minimal attack surface and optimized resource usage.

### Python App
A complete Python development environment with pip package manager and common development libraries. Ideal for developing web applications, data science projects, automation scripts, and machine learning applications.

### TypeScript App
A development environment for TypeScript applications with Node.js runtime and essential development tools. Perfect for developing modern web applications, APIs, and server-side applications with type safety and modern JavaScript features.

### Expo
A comprehensive development environment for building React Native applications using the Expo framework. Includes Expo CLI, development server, and all necessary tools for cross-platform mobile development.

## Prerequisites

- Docker and Docker Compose
- Git
- Make (optional, for using Makefile commands)

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/beamlit/sandbox.git
   cd sandbox
   ```

2. Start the sandbox environment:
   ```bash
   docker-compose up -d <template-name>
   ```

## Usage

### Blaxel SDK

Languages:
 - [Python](https://github.com/beamlit/sdk-python)
 - [Typescript](https://github.com/modelcontextprotocol/typescript-sdk)

You can find sample on how to create/retrieve/remove a Sandbox at this url: https://docs.blaxel.ai/Sandboxes/Overview
Sandbox operations:
- [Process](https://docs.blaxel.ai/Sandboxes/Processes)
- [Filesystem](https://docs.blaxel.ai/Sandboxes/Filesystem)

### Accessing template environments

Each template exposes specific ports for access:

- **Base**: Accessible via port 8080 (sandbox-api)
- **Python App**: Accessible via ports 8080 (sandbox-api) and 8000 (python-app)
- **TypeScript App**: Accessible via ports 8080 (sandbox-api) and 3000 (ts-app)
- **Expo**: Accessible via multiple ports for various Expo services (19000-19006, 8081)

### Developing with the Sandbox API

#### Development server

The recommended way to develop on the Sandbox API is to use the dev environment with Docker Compose:

```bash
docker-compose up dev
```

This will start the development container with the sandbox-api directory mounted as a volume, enabling hot-reloading through Air for real-time code changes. You can then request your sandbox api on port 8080.

After your server has started (You should see a log: "Starting Sandbox API server on :8080"). You can run this command to check everything is running
```bash
curl http://localhost:8080/filesystem/~
```

#### Test server

Go to sandbox-api/integration-tests
[Detailed documentation](sandbox-api/integration-tests/README.md)

## Configuration

Template configurations are defined in `template.json` files within each template directory. These files specify:

- Template name and display name
- Categories and descriptions
- Memory requirements
- Exposed ports and protocols
- Enterprise features and availability status

## Contributing

We welcome contributions to Sandbox Hub! Please follow these steps:

1. Fork the repository
2. Create a new branch (`git checkout -b feature/new-template`)
3. Make your changes
4. Commit your changes (`git commit -m 'Add new template'`)
5. Push to the branch (`git push origin feature/new-template`)
6. Open a Pull Request

### Creating a New Template

To create a new template:

1. Create a new directory in the `hub` folder with your template name
2. Add a `Dockerfile` that sets up the required environment
3. Create a `template.json` file with the template configuration
4. Add your template to docker-compose.yaml
4. Test your template
5. Submit a pull request with your new template

## License

This project is licensed under the MIT License - see the [LICENSE](../LICENSE) file for details.

## Support

For support, please:

- Open an issue on GitHub
- Contact the Blaxel team at support@blaxel.ai
- Join our community channels

---

Built with ❤️ by [Blaxel](https://blaxel.ai)
