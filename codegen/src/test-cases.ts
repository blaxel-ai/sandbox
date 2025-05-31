import { BlaxelMcpClientTransport, SandboxInstance, settings } from "@blaxel/core";
import { Client as ModelContextProtocolClient } from "@modelcontextprotocol/sdk/client/index.js";

export interface ToolCallResult {
  success: boolean;
  error?: string;
  data?: any;
}

export interface TestCase {
  name: string;
  tool: string;
  args: any;
  expectSuccess: boolean;
  description: string;
  setup?: () => Promise<void>;
  cleanup?: () => Promise<void>;
}

export class BlToolsTestSuite {
  private client?: ModelContextProtocolClient;
  private sandbox?: SandboxInstance;
  private sandboxName: string = "mcp-test-sandbox";

  async initialize(): Promise<void> {
    const isProduction = process.env.NODE_ENV === 'production';

    if (isProduction) {
      console.log('🏗️  Production mode: Creating sandbox instance...');

      // Create sandbox instance
      this.sandbox = await SandboxInstance.createIfNotExists({
        metadata: {
          name: this.sandboxName
        },
        spec: {
          runtime: {
            image: "blaxel/prod-nextjs:latest",
            memory: 4096,
            ports: [
              {
                name: "sandbox-api",
                target: 8080,
                protocol: "HTTP",
              },
              {
                name: "preview",
                target: 3000,
                protocol: "HTTP",
              }
            ]
          }
        }
      });

      await this.sandbox.wait();

      // Setup MCP client with sandbox URL
      const url = `${settings.runUrl}/${settings.workspace}/sandboxes/${this.sandboxName}`;
      const transport = new BlaxelMcpClientTransport(
        url.toString(),
        settings.headers,
      );

      this.client = new ModelContextProtocolClient(
        {
          name: "mcp-sandbox-test",
          version: "1.0.0",
        },
        { capabilities: { tools: {} } }
      );

      await this.client.connect(transport);
    } else {
      console.log('🏠 Development mode: Connecting to local WebSocket...');

      // Use local WebSocket connection
      const transport = new BlaxelMcpClientTransport(
        "http://localhost:8080",
        {},
      );

      this.client = new ModelContextProtocolClient(
        {
          name: "mcp-local-test",
          version: "1.0.0",
        },
        { capabilities: { tools: {} } }
      );

      await this.client.connect(transport);
    }
  }

  async cleanup(): Promise<void> {
    if (this.client) {
      await this.client.close();
    }
    // Note: Sandbox cleanup is handled automatically by the Blaxel platform
    // when the sandbox is no longer needed
  }

  async callTool(toolName: string, args: any): Promise<ToolCallResult> {
    if (!this.client) {
      throw new Error("Client not initialized. Call initialize() first.");
    }

    try {
      const result = await this.client.callTool({
        name: toolName,
        arguments: args
      });

      if (result.isError) {
        return {
          success: false,
          error: JSON.stringify(result.content)
        };
      }
      return {
        success: true,
        data: result
      };
    } catch (error) {
      return {
        success: false,
        error: error instanceof Error ? error.message : String(error)
      };
    }
  }

  getTestCases(): TestCase[] {
    return [
      // Setup: Create README.md file for other tests
      {
        name: 'Setup: Create README.md file',
        tool: 'fsWriteFile',
        args: {
          path: '../README.md',
          content: `# Sandbox Project

This is a test README file created by the MCP test suite.

## Features

- File system operations
- Process management
- Network monitoring
- Code generation tools

## Getting Started

1. Start the sandbox API server
2. Connect to the MCP endpoint
3. Use the available tools

## Tools Available

### Filesystem Tools
- fsWriteFile: Create or update files
- fsReadFile: Read file contents
- fsListDirectory: List directory contents

### Process Tools
- Execute commands
- Monitor process logs
- Manage process lifecycle

### Codegen Tools
- Edit files with intelligent code changes
- Search codebase semantically
- Grep search with regex patterns
- Read file ranges

## License

MIT License - This is a test file.
`,
          permissions: '644'
        },
        expectSuccess: true,
        description: 'Creates a README.md file for other tests to use'
      },

      // codegenListDir tests
      {
        name: 'List root directory',
        tool: 'codegenListDir',
        args: { relativeWorkspacePath: '.' },
        expectSuccess: true,
        description: 'Lists contents of the current directory'
      },
      {
        name: 'List non-existent directory',
        tool: 'codegenListDir',
        args: { relativeWorkspacePath: './non-existent-dir' },
        expectSuccess: false,
        description: 'Attempts to list a directory that does not exist'
      },

      // codegenFileSearch tests
      {
        name: 'Search for Go files',
        tool: 'codegenFileSearch',
        args: { query: '*.go' },
        expectSuccess: true,
        description: 'Searches for Go source files'
      },
      {
        name: 'Search for package.json',
        tool: 'codegenFileSearch',
        args: { query: 'package.json' },
        expectSuccess: true,
        description: 'Searches for package.json files'
      },
      {
        name: 'Search for non-existent pattern',
        tool: 'codegenFileSearch',
        args: { query: 'non-existent-file-xyz123' },
        expectSuccess: true,
        description: 'Searches for a pattern that should return no results'
      },

      // codegenCodebaseSearch tests
      {
        name: 'Search for function definitions',
        tool: 'codegenCodebaseSearch',
        args: {
          query: 'func main',
          targetDirectories: ['.']
        },
        expectSuccess: true,
        description: 'Searches for function definitions in the codebase'
      },
      {
        name: 'Search for import statements',
        tool: 'codegenCodebaseSearch',
        args: {
          query: 'import',
          targetDirectories: ['./src']
        },
        expectSuccess: true,
        description: 'Searches for import statements in source code'
      },

      // codegenGrepSearch tests
      {
        name: 'Grep search for TODO comments',
        tool: 'codegenGrepSearch',
        args: {
          query: 'TODO',
          caseSensitive: false,
          includePattern: '*.go'
        },
        expectSuccess: true,
        description: 'Searches for TODO comments in Go files'
      },
      {
        name: 'Grep search with regex pattern',
        tool: 'codegenGrepSearch',
        args: {
          query: 'func\\s+\\w+\\(',
          caseSensitive: true,
          includePattern: '*.go'
        },
        expectSuccess: true,
        description: 'Uses regex to find function declarations'
      },
      {
        name: 'Grep search with invalid regex',
        tool: 'codegenGrepSearch',
        args: {
          query: '[invalid-regex',
          caseSensitive: false
        },
        expectSuccess: false,
        description: 'Tests error handling with invalid regex pattern'
      },

      // codegenReadFileRange tests
      {
        name: 'Read first 10 lines of README',
        tool: 'codegenReadFileRange',
        args: {
          targetFile: '../README.md',
          startLineOneIndexed: 1,
          endLineOneIndexedInclusive: 10
        },
        expectSuccess: true,
        description: 'Reads the first 10 lines of README.md'
      },
      {
        name: 'Read lines beyond file length',
        tool: 'codegenReadFileRange',
        args: {
          targetFile: '../README.md',
          startLineOneIndexed: 1000,
          endLineOneIndexedInclusive: 1010
        },
        expectSuccess: false,
        description: 'Attempts to read lines beyond file length'
      },
      {
        name: 'Read non-existent file',
        tool: 'codegenReadFileRange',
        args: {
          targetFile: './non-existent-file.txt',
          startLineOneIndexed: 1,
          endLineOneIndexedInclusive: 5
        },
        expectSuccess: false,
        description: 'Attempts to read from a non-existent file'
      },
      {
        name: 'Read with invalid line range',
        tool: 'codegenReadFileRange',
        args: {
          targetFile: '../README.md',
          startLineOneIndexed: 0,
          endLineOneIndexedInclusive: 5
        },
        expectSuccess: false,
        description: 'Tests validation with invalid line numbers'
      },

      // codegenEditFile tests
      {
        name: 'Create new test file',
        tool: 'codegenEditFile',
        args: {
          targetFile: './test-output/new-file.txt',
          instructions: 'Creating a new test file with sample content',
          codeEdit: 'Hello, World!\nThis is a test file created by the MCP codegen tools.\nLine 3 of the file.'
        },
        expectSuccess: true,
        description: 'Creates a new file with content',
        setup: async () => {
          // Ensure test-output directory exists
          await this.callTool('codegenRunTerminalCmd', {
            command: 'mkdir -p ./test-output',
            isBackground: false
          });
        }
      },
      {
        name: 'Edit existing file',
        tool: 'codegenEditFile',
        args: {
          targetFile: './test-output/new-file.txt',
          instructions: 'Adding a new line to the existing file',
          codeEdit: 'Hello, World!\nThis is a test file created by the MCP codegen tools.\nLine 3 of the file.\n// ... existing code ...\nThis is a new line added by edit!'
        },
        expectSuccess: true,
        description: 'Edits an existing file by adding content'
      },

      // codegenRunTerminalCmd tests
      {
        name: 'Execute simple command',
        tool: 'codegenRunTerminalCmd',
        args: {
          command: 'echo "Hello from MCP!"',
          isBackground: false
        },
        expectSuccess: true,
        description: 'Executes a simple echo command'
      },
      {
        name: 'Execute command in background',
        tool: 'codegenRunTerminalCmd',
        args: {
          command: 'sleep 2 && echo "Background task completed"',
          isBackground: true
        },
        expectSuccess: true,
        description: 'Executes a command in background mode'
      },
      {
        name: 'Execute invalid command',
        tool: 'codegenRunTerminalCmd',
        args: {
          command: 'nonexistentcommand123',
          isBackground: false
        },
        expectSuccess: false,
        description: 'Tests error handling with invalid commands'
      },

      // fsWriteFile test (this is a filesystem tool, not codegen)
      {
        name: 'Write file using fsWriteFile',
        tool: 'fsWriteFile',
        args: {
          path: '/blaxel/tmp/testfile_.txt',
          content: 'Test content from test suite'
        },
        expectSuccess: true,
        description: 'Writes a file using the fsWriteFile tool'
      },

      // codegenReapply tests
      {
        name: 'Test reapply functionality',
        tool: 'codegenReapply',
        args: {
          targetFile: './test-output/new-file.txt'
        },
        expectSuccess: true,
        description: 'Tests the reapply tool (currently a placeholder)'
      }
    ];
  }

  async runTestCase(testCase: TestCase): Promise<{
    testCase: TestCase;
    result: ToolCallResult;
    setupError?: string;
    cleanupError?: string;
  }> {
    let setupError: string | undefined;
    let cleanupError: string | undefined;

    // Run setup if provided
    if (testCase.setup) {
      try {
        await testCase.setup();
      } catch (error) {
        setupError = error instanceof Error ? error.message : String(error);
      }
    }

    // Run the actual test
    const result = await this.callTool(testCase.tool, testCase.args);

    // Run cleanup if provided
    if (testCase.cleanup) {
      try {
        await testCase.cleanup();
      } catch (error) {
        cleanupError = error instanceof Error ? error.message : String(error);
      }
    }

    return {
      testCase,
      result,
      setupError,
      cleanupError
    };
  }

  async runAllTests(): Promise<void> {
    console.log('🚀 Initializing sandbox and MCP client...\n');

    try {
      await this.initialize();
    } catch (error) {
      console.error('❌ Failed to initialize sandbox:', error instanceof Error ? error.message : String(error));
      return;
    }

    const testCases = this.getTestCases();
    const results: any[] = [];

    console.log(`🧪 Running ${testCases.length} test cases...\n`);

    for (let i = 0; i < testCases.length; i++) {
      const testCase = testCases[i];

      console.log(`[${i + 1}/${testCases.length}] Running: ${testCase.name}`);
      console.log(`   Tool: ${testCase.tool}`);
      console.log(`   Description: ${testCase.description}`);

      try {
        const testResult = await this.runTestCase(testCase);

        // Check if result matches expectation
        const passed = testResult.result.success === testCase.expectSuccess;

        if (passed) {
          console.log(`   ✅ PASSED ${testResult.result.success ? '(Success)' : '(Expected failure)'}`);
        } else {
          console.log(`   ❌ FAILED - Expected ${testCase.expectSuccess ? 'success' : 'failure'}, got ${testResult.result.success ? 'success' : 'failure'}`);
          if (testResult.result.error) {
            console.log(`   Error: ${testResult.result.error}`);
          }
        }

        if (testResult.setupError) {
          console.log(`   ⚠️  Setup error: ${testResult.setupError}`);
        }

        if (testResult.cleanupError) {
          console.log(`   ⚠️  Cleanup error: ${testResult.cleanupError}`);
        }

        results.push({
          ...testResult,
          passed
        });

      } catch (error) {
        console.log(`   💥 EXCEPTION: ${error instanceof Error ? error.message : String(error)}`);
        results.push({
          testCase,
          result: { success: false, error: String(error) },
          passed: false
        });
      }

      console.log('');
    }

    // Summary
    const passed = results.filter(r => r.passed).length;
    const failed = results.length - passed;

    console.log('📊 Test Results Summary:');
    console.log(`   ✅ Passed: ${passed}`);
    console.log(`   ❌ Failed: ${failed}`);
    console.log(`   📝 Total: ${results.length}`);

    if (failed > 0) {
      console.log('\n❌ Failed tests:');
      results
        .filter(r => !r.passed)
        .forEach(r => {
          console.log(`   • ${r.testCase.name}: ${r.result.error || 'Unexpected result'}`);
        });
    }

    // Cleanup
    console.log('\n🧹 Cleaning up...');
    try {
      await this.cleanup();
      console.log('✅ Cleanup completed');
    } catch (error) {
      console.error('❌ Cleanup failed:', error instanceof Error ? error.message : String(error));
    }
  }
}