import { config } from 'dotenv';
config();

import { BlaxelMcpClientTransport, BlaxelMcpServerTransport } from "@blaxel/sdk";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

// Helper function to convert JSON Schema to Zod schema
function jsonSchemaToZod(schema: any): Record<string, z.ZodTypeAny> {
  if (!schema || typeof schema !== 'object' || schema.type !== 'object' || !schema.properties) {
    return {};
  }

  const properties: Record<string, z.ZodTypeAny> = {};

  Object.entries(schema.properties).forEach(([key, value]: [string, any]) => {
    if (value.type === 'string') {
      properties[key] = z.string();
    } else if (value.type === 'number') {
      properties[key] = z.number();
    } else if (value.type === 'boolean') {
      properties[key] = z.boolean().default(false);
    } else if (value.type === 'array') {
      properties[key] = z.array(z.any());
    } else if (value.type === 'object') {
      properties[key] = z.object(jsonSchemaToZod(value));
    } else {
      properties[key] = z.any();
    }
    if (value.default) {
      properties[key] = properties[key].default(value.default);
    }
    if (!schema.required || !schema.required.includes(key)) {
      properties[key] = properties[key].optional();
    }
  });

  return properties;
}

const server = new McpServer({
  name: "mcp-blaxel-sandbox",
  version: "1.0.0",
  description: "MCP server for Blaxel Sandbox",
});

// Get the Blaxel MCP URL from environment variables or use a default
const blaxelMcpUrl = process.env.BLAXEL_MCP_URL || "http://localhost:8080";

const clientTransport = new BlaxelMcpClientTransport(
  blaxelMcpUrl
);

const client = new Client(
  {
    name: "mcp-client",
    version: "1.0.0"
  },
  {
    capabilities: {
      tools: {}
    }
  }
);


async function main() {
  let transport;
  if (process.env.BL_SERVER_PORT) {
    transport = new BlaxelMcpServerTransport();
  } else {
    transport = new StdioServerTransport();
  }
  await client.connect(clientTransport);
  const {tools} = await client.listTools();
  for (const tool of tools) {
    // Create a default empty schema if inputSchema is null or invalid
    const zodSchema = tool.inputSchema ? jsonSchemaToZod(tool.inputSchema) : {};

    server.tool(tool.name, tool.description, zodSchema, async (params) => {
      await client.connect(clientTransport);
      try {
        const result = await client.callTool({
          name: tool.name,
          arguments: params
        });
        await client.close();
        return result;
      } catch (error) {
        console.error(error);
        await client.close();
        return {
          error: "An error occurred while calling the tool"
        };
      }
    });
  }
  await client.close();
  server.connect(transport);
}

main();
