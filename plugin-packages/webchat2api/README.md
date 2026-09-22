# web2api 网页模型中转（webchat2api）

把 [NZX2007/webchat2api](https://github.com/NZX2007/webchat2api) 的本地网页版模型中转服务接入影策的独立请求协议插件。该插件在同一个包内贡献四个文本 provider，分别对应 webchat2api 的四个本地服务：

| Provider ID | 上游服务 | 默认 Base URL | 常用模型 |
| --- | --- | --- | --- |
| `webchat2api-qwen` | qwen2api（通义千问网页版） | `http://127.0.0.1:8301` | `qwen3.7-think` 等，完整列表见 `GET /v1/models` |
| `webchat2api-doubao` | doubao2api（豆包网页版） | `http://127.0.0.1:8302` | `doubao-web`、`doubao-web-expert` |
| `webchat2api-mimo` | mimo2api（小米 MiMo Studio） | `http://127.0.0.1:8303` | `mimo-v2.5-pro`、`mimo-v2.5` |
| `webchat2api-deepseek` | ds2api（DeepSeek 网页版） | `http://127.0.0.1:5001` | `deepseek-v4-flash`、`deepseek-v4-pro`（含 `-nothinking` / `-search` 变体） |

完整接口见 [docs/interface.md](docs/interface.md)。

## 使用前提

1. 按上游项目 README 部署并启动对应服务（`python setup_wizard.py qwen | doubao | mimo`，DeepSeek 见 `services/ds2api/README.md`），拿到各服务 `config.json` 中自动生成的 `api_key`（形如 `sk-qwenweb-xxxx`）。
2. 所有服务只监听 `127.0.0.1`。影策后端默认拒绝本机/私网上游，需要在启动后端时精确放行本机回环地址，例如：

   ```
   CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS=127.0.0.1,localhost
   ```

3. 在影策中为对应 provider 创建渠道：Base URL 填服务地址（默认端口不同，可在渠道中修改），鉴权选择 API Key 并填入第 1 步的 `api_key`。

## 协议说明

四个服务统一暴露 OpenAI 协议 `POST /v1/chat/completions`（另支持 Anthropic 协议，本插件按 OpenAI 协议声明）。流式由上游支持，但影策后台任务当前以最终响应归一。凭据失效时上游返回结构化错误码（`cookie_missing` / `cookie_expired` / `rate_limited` / `risk_control` / `upstream_error`），会通过 `error.code` / `error.message` 透出。
