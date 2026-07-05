# onvif-proxy

Go 编写的 RTSP → ONVIF 虚拟设备代理:把任意 RTSP 流包装成 ONVIF Profile S 虚拟摄像机,供 Unifi Protect 等客户端发现/收编。带内嵌 Web UI(配置编辑、RTSP 探测、快照、MJPEG 预览、ONVIF 自检)。

## 当前阶段

**核心实现已完成并通过冒烟验证**(全部包单测绿、内置 ONVIF 自检全绿、Docker 可构建);待 Unifi Protect 实机验证。改代码前先读 `docs/` 下设计文档(`docs/06-internal-api.md` 是包间签名契约),实现必须与文档一致;若实现中发现文档不合理,先改文档再写码。

## 设计文档(真相来源)

- `docs/01-architecture.md` — 模块划分、数据流、目录结构、技术选型(含"为什么不用 WSDL 代码生成")
- `docs/02-onvif-spec.md` — ONVIF 方法清单、Fault 语义、WSSE、WS-Discovery 细节;末尾有验收基准表
- `docs/03-config.md` — YAML 配置格式与校验规则
- `docs/04-web-api.md` — REST API 契约与 UI 设计
- `docs/05-deployment.md` — Dockerfile、macvlan/host/bridge 三种网络模式
- `docs/07-mcp.md` — MCP 服务端点(基于官方 go-sdk 的 Streamable HTTP `/mcp`、工具清单、实现契约)

## 硬性约束

- **规范优先**:任何 SOAP 响应(含错误)必须是格式合法的 SOAP 1.2 报文。未实现方法返回 `ter:ActionNotSupported` Fault,禁止裸 500 —— 这是本项目区别于上游(daniela-hase/onvif-server 及 p10tyr fork)的立项原因。
- `GetSystemDateAndTime` 永远免认证(ONVIF Core Spec 要求)。
- 依赖最小化:第三方库仅 `gopkg.in/yaml.v3` 与 `github.com/modelcontextprotocol/go-sdk`(后者仅用于 `/mcp` 端点,见 `docs/07-mcp.md`;不得因此引入其他直接依赖);UUID/MAC 用 `crypto/rand` 自造;SOAP 用手写 XML 模板 + `encoding/xml` token 流解析。
- 视频路径零转码:RTSP 走 TCP 透传;ffmpeg 只用于快照和 UI 预览,且通过 `exec.Command` 参数传递(无 shell)。
- uuid/mac 首次生成后写回 config.yaml 持久化,保证设备身份稳定。

## 常用命令

```bash
go build ./...                 # 构建
go test ./...                  # 测试
go vet ./...                   # 静态检查
go run ./cmd/onvif-proxy -config config.yaml   # 本地运行(快照/预览需 PATH 有 ffmpeg)
```

## 验收方式

实现后跑 Web UI 的"ONVIF 自检"(`POST /api/test/onvif`),对照 `docs/02-onvif-spec.md` 第 7 节的基准表,必须全绿;另需覆盖:故意调用一个不存在的方法,验证返回规范 Fault 而非裸 500。
