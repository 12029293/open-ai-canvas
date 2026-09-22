# web2api 网页模型中转 接口字段

## 协议身份

- 插件 ID：`webchat2api`。
- Provider ID：`webchat2api-qwen`、`webchat2api-doubao`、`webchat2api-mimo`、`webchat2api-deepseek`（同一包内四个 provider，协议完全一致，仅默认 Base URL 不同）。
- 能力：`text`。
- 默认 Base URL：`http://127.0.0.1:8301`（qwen）/ `http://127.0.0.1:8302`（doubao）/ `http://127.0.0.1:8303`（mimo）/ `http://127.0.0.1:5001`（deepseek）。
- 鉴权驱动：`bearer`（`Authorization: Bearer <apiKey>`）。
- 创建：`POST /v1/chat/completions`。
- Agent：`POST /v1/chat/completions`。
- 生命周期：同步响应。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | webchat2api 各服务 `config.json` 中的 `api_key`（形如 `sk-qwenweb-xxx`）。 |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | 上游模型 ID。 |
| `messages` | message[] | 是 | `provider message container` | 包含历史消息和当前用户输入。 |
| `instructions` | string | 否 | `system/instructions` | 系统指令。 |
| `temperature` | number | 否 | `temperature` | 采样温度。 |
| `top_p` | number | 否 | `top_p` | 核采样参数。 |
| `max_tokens` | integer | 否 | `max_tokens/max_output_tokens` | 最大输出 token。 |
| `tools` | array | 否 | `tools/toolConfig` | 工具定义。 |
| `tool_choice` | object|string | 否 | `tool_choice` | 工具选择策略。 |
| `response_format` | object | 否 | `response_format/text` | 结构化输出配置。 |
| `stream` | boolean | 否 | `stream` | 流式开关；后台任务当前以最终响应归一。 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。表中 `<provider>` 为四个 provider ID 之一（`webchat2api-qwen`、`webchat2api-doubao`、`webchat2api-mimo`、`webchat2api-deepseek`）。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/v1/chat/completions"` |
| `create.contentType` | `"application/json"` |
| `create.body.model` | `{"$ref":"request.model"}` |
| `create.body.messages` | `{"$ref":"request.messages"}` |
| `create.body.temperature` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.temperature"}}` |
| `create.body.top_p` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.top_p"}}` |
| `create.body.max_tokens` | `{"$omitEmpty":{"$coalesce":[{"$ref":"request.extra.max_tokens"},{"$ref":"request.providerOptions.<provider>.max_tokens"}]}}` |
| `create.body.tools` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.tools"}}` |
| `create.body.tool_choice` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.tool_choice"}}` |
| `create.body.response_format` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.response_format"}}` |
| `create.body.stream` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.stream"}}` |
| `create.body.extra_body` | `{"$omitEmpty":{"$ref":"request.providerOptions.<provider>.extra_body"}}` |
| `agent.method` | `"POST"` |
| `agent.path` | `"/v1/chat/completions"` |
| `agent.body` | `{"$merge":[{"$ref":"request.extra.agent.chatCompletion"},{"model":{"$ref":"request.model"}}]}` |

## 响应映射

同步 JSON 响应按以下路径解析；解析失败或空结果按失败处理。

| 结果字段 | 上游路径 |
| --- | --- |
| 文本 | `choices.0.message.content`、`choices.0.text` |
| 思考过程 | `choices.0.message.reasoning_content` |
| 用量 | `response.usage` |
| 错误码 | `error.code`（上游 `cookie_missing` / `cookie_expired` / `rate_limited` / `risk_control` / `upstream_error`） |
| 错误消息 | `error.message` |

Agent（工具调用）响应额外映射：

| 结果字段 | 上游路径 |
| --- | --- |
| 工具调用列表 | `choices.0.message.tool_calls` |
| 工具调用 ID | `id` |
| 工具名称 | `function.name` |
| 工具参数 | `function.arguments` |

上游返回非 2xx 或缺少文本结果时，任务按失败归一，并把 `error.code` / `error.message` 作为失败原因透出。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "webchat2api",
  "name": "web2api 网页模型中转",
  "version": "1.0.0",
  "author": "NZX2007 webchat2api / 影策",
  "description": "webchat2api 本地网页版模型中转（Qwen / 豆包 / MiMo / DeepSeek）独立请求协议插件。",
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true,
        "description": "webchat2api 各服务 config.json 中的 api_key（形如 sk-qwenweb-xxx）。"
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "webchat2api-qwen",
        "label": "web2api · Qwen 通义千问",
        "capabilities": [
          "text"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "http://127.0.0.1:8301",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游模型 ID。"
          },
          {
            "name": "messages",
            "type": "message[]",
            "required": true,
            "mapping": "provider message container",
            "description": "包含历史消息和当前用户输入。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "system/instructions",
            "description": "系统指令。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "top_p",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "max_tokens/max_output_tokens",
            "description": "最大输出 token。"
          },
          {
            "name": "tools",
            "type": "array",
            "required": false,
            "mapping": "tools/toolConfig",
            "description": "工具定义。"
          },
          {
            "name": "tool_choice",
            "type": "object|string",
            "required": false,
            "mapping": "tool_choice",
            "description": "工具选择策略。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "response_format/text",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "stream",
            "description": "流式开关；后台任务当前以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "messages": {
              "$ref": "request.messages"
            },
            "temperature": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.temperature"
              }
            },
            "top_p": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.top_p"
              }
            },
            "max_tokens": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.extra.max_tokens"
                  },
                  {
                    "$ref": "request.providerOptions.webchat2api-qwen.max_tokens"
                  }
                ]
              }
            },
            "tools": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.tools"
              }
            },
            "tool_choice": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.tool_choice"
              }
            },
            "response_format": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.response_format"
              }
            },
            "stream": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.stream"
              }
            },
            "extra_body": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-qwen.extra_body"
              }
            }
          }
        },
        "agent": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "$ref": "request.extra.agent.chatCompletion"
              },
              {
                "model": {
                  "$ref": "request.model"
                }
              }
            ]
          }
        },
        "response": {
          "status": "succeeded",
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        },
        "agentResponse": {
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "toolCallsPath": "choices.0.message.tool_calls",
          "toolCallIdPaths": [
            "id"
          ],
          "toolCallNamePaths": [
            "function.name"
          ],
          "toolCallArgumentsPaths": [
            "function.arguments"
          ]
        }
      },
      {
        "id": "webchat2api-doubao",
        "label": "web2api · 豆包",
        "capabilities": [
          "text"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "http://127.0.0.1:8302",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游模型 ID。"
          },
          {
            "name": "messages",
            "type": "message[]",
            "required": true,
            "mapping": "provider message container",
            "description": "包含历史消息和当前用户输入。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "system/instructions",
            "description": "系统指令。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "top_p",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "max_tokens/max_output_tokens",
            "description": "最大输出 token。"
          },
          {
            "name": "tools",
            "type": "array",
            "required": false,
            "mapping": "tools/toolConfig",
            "description": "工具定义。"
          },
          {
            "name": "tool_choice",
            "type": "object|string",
            "required": false,
            "mapping": "tool_choice",
            "description": "工具选择策略。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "response_format/text",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "stream",
            "description": "流式开关；后台任务当前以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "messages": {
              "$ref": "request.messages"
            },
            "temperature": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.temperature"
              }
            },
            "top_p": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.top_p"
              }
            },
            "max_tokens": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.extra.max_tokens"
                  },
                  {
                    "$ref": "request.providerOptions.webchat2api-doubao.max_tokens"
                  }
                ]
              }
            },
            "tools": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.tools"
              }
            },
            "tool_choice": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.tool_choice"
              }
            },
            "response_format": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.response_format"
              }
            },
            "stream": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.stream"
              }
            },
            "extra_body": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-doubao.extra_body"
              }
            }
          }
        },
        "agent": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "$ref": "request.extra.agent.chatCompletion"
              },
              {
                "model": {
                  "$ref": "request.model"
                }
              }
            ]
          }
        },
        "response": {
          "status": "succeeded",
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        },
        "agentResponse": {
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "toolCallsPath": "choices.0.message.tool_calls",
          "toolCallIdPaths": [
            "id"
          ],
          "toolCallNamePaths": [
            "function.name"
          ],
          "toolCallArgumentsPaths": [
            "function.arguments"
          ]
        }
      },
      {
        "id": "webchat2api-mimo",
        "label": "web2api · 小米 MiMo",
        "capabilities": [
          "text"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "http://127.0.0.1:8303",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游模型 ID。"
          },
          {
            "name": "messages",
            "type": "message[]",
            "required": true,
            "mapping": "provider message container",
            "description": "包含历史消息和当前用户输入。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "system/instructions",
            "description": "系统指令。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "top_p",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "max_tokens/max_output_tokens",
            "description": "最大输出 token。"
          },
          {
            "name": "tools",
            "type": "array",
            "required": false,
            "mapping": "tools/toolConfig",
            "description": "工具定义。"
          },
          {
            "name": "tool_choice",
            "type": "object|string",
            "required": false,
            "mapping": "tool_choice",
            "description": "工具选择策略。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "response_format/text",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "stream",
            "description": "流式开关；后台任务当前以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "messages": {
              "$ref": "request.messages"
            },
            "temperature": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.temperature"
              }
            },
            "top_p": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.top_p"
              }
            },
            "max_tokens": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.extra.max_tokens"
                  },
                  {
                    "$ref": "request.providerOptions.webchat2api-mimo.max_tokens"
                  }
                ]
              }
            },
            "tools": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.tools"
              }
            },
            "tool_choice": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.tool_choice"
              }
            },
            "response_format": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.response_format"
              }
            },
            "stream": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.stream"
              }
            },
            "extra_body": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-mimo.extra_body"
              }
            }
          }
        },
        "agent": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "$ref": "request.extra.agent.chatCompletion"
              },
              {
                "model": {
                  "$ref": "request.model"
                }
              }
            ]
          }
        },
        "response": {
          "status": "succeeded",
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        },
        "agentResponse": {
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "toolCallsPath": "choices.0.message.tool_calls",
          "toolCallIdPaths": [
            "id"
          ],
          "toolCallNamePaths": [
            "function.name"
          ],
          "toolCallArgumentsPaths": [
            "function.arguments"
          ]
        }
      },
      {
        "id": "webchat2api-deepseek",
        "label": "web2api · DeepSeek",
        "capabilities": [
          "text"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "http://127.0.0.1:5001",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游模型 ID。"
          },
          {
            "name": "messages",
            "type": "message[]",
            "required": true,
            "mapping": "provider message container",
            "description": "包含历史消息和当前用户输入。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "system/instructions",
            "description": "系统指令。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "top_p",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "max_tokens/max_output_tokens",
            "description": "最大输出 token。"
          },
          {
            "name": "tools",
            "type": "array",
            "required": false,
            "mapping": "tools/toolConfig",
            "description": "工具定义。"
          },
          {
            "name": "tool_choice",
            "type": "object|string",
            "required": false,
            "mapping": "tool_choice",
            "description": "工具选择策略。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "response_format/text",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "stream",
            "description": "流式开关；后台任务当前以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "messages": {
              "$ref": "request.messages"
            },
            "temperature": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.temperature"
              }
            },
            "top_p": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.top_p"
              }
            },
            "max_tokens": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.extra.max_tokens"
                  },
                  {
                    "$ref": "request.providerOptions.webchat2api-deepseek.max_tokens"
                  }
                ]
              }
            },
            "tools": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.tools"
              }
            },
            "tool_choice": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.tool_choice"
              }
            },
            "response_format": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.response_format"
              }
            },
            "stream": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.stream"
              }
            },
            "extra_body": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.webchat2api-deepseek.extra_body"
              }
            }
          }
        },
        "agent": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "$ref": "request.extra.agent.chatCompletion"
              },
              {
                "model": {
                  "$ref": "request.model"
                }
              }
            ]
          }
        },
        "response": {
          "status": "succeeded",
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        },
        "agentResponse": {
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "toolCallsPath": "choices.0.message.tool_calls",
          "toolCallIdPaths": [
            "id"
          ],
          "toolCallNamePaths": [
            "function.name"
          ],
          "toolCallArgumentsPaths": [
            "function.arguments"
          ]
        }
      }
    ]
  },
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>"
}
```
<!-- YINGCE_MANIFEST_CONTRACT_END -->
