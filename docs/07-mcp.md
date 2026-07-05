# 07 — MCP 服务端点(Model Context Protocol)

把 onvif-proxy 的管理能力(设备增删改查、RTSP 探测、快照、ONVIF 自检等)以
MCP(Model Context Protocol)工具的形式暴露给 AI 客户端(Claude Code / Claude
Desktop 等),使其可以直接管理本代理:例如"帮我把这个 RTSP 流加成虚拟摄像机
并跑一遍自检"。

- 实现:**官方 Go SDK** `github.com/modelcontextprotocol/go-sdk`(v1.6+,
  Google 合作维护)。这是项目"第三方依赖仅 yaml.v3"约束的**唯一例外**,
  理由:MCP 协议面(生命周期/会话/SSE/多版本协商)由官方库长期跟进,自造收益低;
  该例外已同步写入 CLAUDE.md 与 docs/01/06。
- 传输:Streamable HTTP,单端点 `/mcp`,挂在现有 Web UI 的 mux 上
  (与 REST API 同端口),**无状态模式**(`Stateless: true`,不签发
  `Mcp-Session-Id`)。协议版本协商、405/会话/SSE 细节全部由 SDK 处理。
- 认证:复用 `withAuth` —— 配置了 `web.username` 时全站 Basic 认证,`/mcp`
  自动被覆盖。
- Go 版本:SDK 要求 Go ≥ 1.25,go.mod 与 Dockerfile 构建镜像同步上调
  (`golang:1.26-alpine`,见 docs/05)。

客户端接入示例:

```bash
claude mcp add --transport http onvif-proxy http://<host>:8080/mcp
# 开启了 web basic auth 时:
claude mcp add --transport http onvif-proxy http://<host>:8080/mcp \
  --header "Authorization: Basic $(printf '%s:%s' user pass | base64)"
```

## 1. 能力声明

`initialize` 响应由 SDK 生成;`serverInfo` 为
`{name: "onvif-proxy", version: <Status().Version>}`;仅注册 tools 能力
(不提供 resources/prompts);`ServerOptions.Instructions` 设为:

> onvif-proxy management server: wrap RTSP streams as virtual ONVIF cameras.
> Use list_devices/get_status to inspect, probe_rtsp/get_stream_info before
> adding a device, add_device/update_device/delete_device to manage,
> run_onvif_selftest to verify.

## 2. 工具集

12 个工具,全部注册在同一个 `mcp.Server` 上。JSON 结果以 text 块承载(序列化
后的 JSON 字符串);快照以 image 块承载。输入结构体用 `jsonschema` tag 描述,
schema 由 SDK 自动生成。

| 工具 | 参数 | 结果 | 对应能力 |
|---|---|---|---|
| `get_status` | 无 | Status JSON(version/advertise_ip/uptime_seconds/ffmpeg) | `Backend.Status` |
| `list_devices` | 无 | deviceView 数组 JSON(与 `GET /api/devices` 同构) | `Backend.Devices` |
| `get_config` | 无 | config.yaml 原文(text) | `Backend.ConfigYAML` |
| `apply_config` | `yaml` string 必填;`dry_run` bool 默认 false | 成功:`"applied"` / `"valid"`;校验失败:isError + 错误原文 | `Backend.ApplyConfig` |
| `add_device` | `device` object 必填(结构同 `POST /api/devices` body,见 docs/04) | 成功:`"applied"`;校验失败:isError | 复用 `buildDevice` + `ApplyConfig` |
| `update_device` | `uuid` string 必填;`device` object 必填 | 同上;uuid 不存在:isError | 复用编辑逻辑(保留 uuid/mac/serial/info;auth 密码留空且用户名不变 = 密码不变) |
| `delete_device` | `uuid` string 必填 | 同上 | 复用删除逻辑 |
| `probe_rtsp` | `url` string 必填 | rtsp.Result JSON | `rtsp.Probe` |
| `get_stream_info` | `url` string 必填 | ffprobe 流信息 JSON;ffmpeg 缺失:isError | 同 `POST /api/test/streaminfo` |
| `take_snapshot` | `uuid` string 必填;`stream` string 可选 | image 块(base64 JPEG);失败/无 ffmpeg:isError | `Backend.Snapshot` |
| `run_onvif_selftest` | `uuid` string 必填 | `[]onvifCheck` JSON(method/http_status/soap_fault/pass) | `runONVIFSelfTest` |
| `get_discovery_log` | 无 | WS-Discovery 日志 JSON | `Backend.DiscoveryLog` |

错误语义:

- **参数非法 / 未知工具** → SDK 层协议错误(客户端收到 JSON-RPC error)。
- **业务失败**(探测失败、配置校验不通过、ffmpeg 缺失、uuid 不存在等)→
  工具结果 `isError: true`,content 为错误原文。在 go-sdk 中,handler 返回
  非 nil error 即映射为 isError 结果,直接利用该机制。

## 3. 实现契约(internal/web)

新文件 `mcp.go` + `mcp_test.go`,`devices.go` 两处小重构;REST 行为不变。

**`mcp.go`**:

- `func (s *Server) mcpHandler() http.Handler`:构建 `mcp.Server`
  (`mcp.NewServer(&mcp.Implementation{...}, &mcp.ServerOptions{Instructions: ...})`),
  `mcp.AddTool` 注册 §2 全部工具,返回
  `mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { ... }, &mcp.StreamableHTTPOptions{Stateless: true})`。
- 工具 handler 统一形态:输入为带 `json`/`jsonschema` tag 的结构体;文本结果用
  小助手 `mcpJSON(v any) *mcp.CallToolResult` 包装(string 原样,其余
  `json.Marshal`)。
- 每设备工具(take_snapshot / run_onvif_selftest / update / delete)先
  `s.findDevice(uuid)`,不存在返回 error("no configured device with that uuid")。
- **API 以 `go doc github.com/modelcontextprotocol/go-sdk/mcp <Symbol>` 输出为
  准**(AddTool 泛型签名、CallToolResult/Content 字段、StreamableHTTPOptions),
  不要凭记忆写。
- SDK 若未内置 Origin 校验(防 DNS rebinding,MCP 规范 MUST 项),在挂载处包一
  层:带 `Origin` 头且其 hostname 既非回环(localhost/127.0.0.1/::1)也不等于
  请求 `Host` 的 hostname 时回 403;不带 `Origin` 放行。

**`web.go` 注册**(`New` 中):`mux.Handle("/mcp", s.mcpHandler())`
(不限方法,GET/DELETE 语义由 SDK 处理)。

**`devices.go` 重构**:

- 提取 `func (s *Server) deviceViews() []deviceView`,`handleDevices` 与
  `list_devices` 共用。
- 提取 `func (s *Server) applyParsedConfig(cfg *config.Config) error`
  (marshal + `Backend.ApplyConfig`),原 `applyConfig(w, cfg)` 改为调用它;
  MCP 的 add/update/delete_device 也调用它。

并发安全:一切配置变更都经 `Backend.ApplyConfig`(main 的 manager 内有锁),
MCP 不引入新的写路径。

**`mcp_test.go`** 覆盖(经 `httptest` + SDK 客户端
`mcp.NewClient` + `mcp.StreamableClientTransport` 走真实 HTTP):

- initialize 成功,serverInfo.name = "onvif-proxy";
- tools/list 恰好 12 个工具、名称齐全;
- `list_devices` / `get_status` 返回与 fake Backend 一致;
- `apply_config` dry_run 校验失败 → isError;
- `delete_device` 未知 uuid → isError;
- 未知工具调用 → 错误;
- 直接 `GET /mcp`(无会话)不会 panic,返回 4xx;
- 带恶意 `Origin` 的 POST → 403(若实现了包装层)。

## 4. 验收

1. `go build ./... && go vet ./... && go test ./...` 全绿;`go.mod` 仅新增
   go-sdk 及其间接依赖。
2. 本地起服务后,用真实客户端接入:
   `claude mcp add --transport http onvif-proxy http://127.0.0.1:8080/mcp`,
   能完成 initialize、列出 12 个工具、成功调用 `get_status` 与
   `list_devices`。
3. Web UI 原有 REST API 与页面行为不变(回归:内置 ONVIF 自检全绿)。
