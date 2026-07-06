# 01 · 总体架构

[English](01-architecture.md) | **简体中文**

## 1. 设计目标

1. **规范优先**:严格按 ONVIF Core Spec / Media Service Spec / SOAP 1.2 / WS-Discovery 实现,所有响应(包括错误)都是格式合法的 SOAP 报文;不出现"未实现方法 → 裸 HTTP 500"。
2. **零转码**:视频流走 TCP 透传,代理本身不解码不转码;ffmpeg 仅用于快照与 UI 预览这类低频操作。
3. **单二进制**:Go 编译产物自包含(Web UI 静态资源用 `go:embed` 打进二进制),Docker 镜像只需再加一个 ffmpeg。
4. **配置即真相**:YAML 配置文件是唯一持久化状态;Web UI 的修改最终落盘到同一个 YAML,重启无损。

## 2. 进程内组件

```
                                ┌────────────────────────────────────────────┐
                                │                onvif-proxy(单进程)          │
                                │                                            │
 ONVIF 客户端                    │  ┌──────────────┐      ┌────────────────┐  │
 (Unifi Protect…)               │  │ WS-Discovery │      │  Config Store  │  │
    │  UDP 3702 组播             │  │  UDP 3702    │      │  (YAML 读写、    │  │
    ├───────────────────────────┼─▶│  Hello/Bye/  │      │   校验、热重载)   │  │
    │                           │  │  ProbeMatch  │      └───────┬────────┘  │
    │                           │  └──────────────┘              │           │
    │  HTTP POST /onvif/*       │  ┌──────────────────────────┐  │           │
    ├───────────────────────────┼─▶│ Virtual Device × N       │◀─┘           │
    │                           │  │ ┌──────────────────────┐ │              │
    │                           │  │ │ SOAP HTTP Server     │ │              │
    │                           │  │ │  - Device Service    │ │              │
    │                           │  │ │  - Media Service     │ │              │
    │                           │  │ │  - /snapshot (JPEG)  │ │              │
    │  RTSP (TCP)               │  │ └──────────────────────┘ │              │
    ├───────────────────────────┼─▶│ ┌──────────────────────┐ │   TCP 透传    │    ┌──────────┐
    │                           │  │ │ RTSP TCP Proxy       │─┼──────────────┼───▶│ 真实摄像头  │
    │                           │  │ └──────────────────────┘ │              │    │ RTSP 源   │
    │                           │  └──────────────────────────┘              │    └──────────┘
 浏览器                          │  ┌──────────────────────────┐              │         ▲
    │  HTTP :8080               │  │ Web Server               │              │         │
    └───────────────────────────┼─▶│  - REST API              │              │         │
                                │  │  - 内嵌静态 UI (embed)     │              │         │
                                │  │  - /mcp (12 个 MCP 工具)   │              │         │
                                │  │  - RTSP Probe 客户端       │─────────────┼─────────┤ RTSP DESCRIBE
                                │  │  - ffmpeg 快照/MJPEG 预览  │─────────────┼─────────┘ 拉流
                                │  └──────────────────────────┘              │
                                └────────────────────────────────────────────┘
```

### 组件职责

| 组件 | 职责 |
|------|------|
| **Config Store** | 加载/校验/原子写回 YAML;为缺省字段生成持久化默认值(UUID、MAC);向 Manager 发布重载事件;加载后应用 `ApplyEnvOverrides`(`ONVIF_*` 环境变量运行时覆盖,仅在内存中生效、绝不写回 YAML,见 `internal/config/env.go`、docs/03-config.zh-CN.md §3) |
| **Device Manager** | 按配置生成 N 个 Virtual Device 实例;负责启停与热重载(先停后起,端口重绑定) |
| **Virtual Device** | 一台虚拟 ONVIF 摄像机 = 1 个 SOAP HTTP 端口 + 1 个 RTSP 代理端口;持有本设备的 profile/编码参数/目标 RTSP 地址 |
| **SOAP 层** | 解析 SOAP Envelope(方法名、WSSE 头、参数),路由到 handler,渲染 XML 响应模板;统一 Fault 生成 |
| **WS-Discovery** | 单个 UDP 监听器代答所有虚拟设备;实现 Hello/Bye/Probe→ProbeMatches |
| **RTSP TCP Proxy** | `io.Copy` 双向透传到真实摄像头的 RTSP 端口;RTSP 认证端到端(客户端凭证直达真实摄像头) |
| **Web Server** | REST API + 内嵌 UI;RTSP 原生探测(OPTIONS/DESCRIBE + Digest 认证);调用 ffmpeg 提供快照与 MJPEG 预览;ONVIF 自检;内嵌 `/mcp` 端点,暴露 12 个 MCP 工具(设备增删改查、RTSP 探测、快照、ONVIF 自检等),详见 docs/07-mcp.zh-CN.md |

## 3. 关键数据流

### 3.1 发现与收编(Unifi Protect 视角)

```
Protect ──UDP Probe──▶ WS-Discovery ──ProbeMatches(XAddrs=http://ip:port/onvif/device_service)──▶ Protect
Protect ──GetSystemDateAndTime(免认证,校时)──▶ Device Service
Protect ──GetCapabilities / GetServices(带 WSSE)──▶ Device Service(返回 Media XAddr)
Protect ──GetProfiles / GetStreamUri──▶ Media Service(返回 rtsp://ip:rtspProxyPort/<path>)
Protect ──RTSP DESCRIBE/SETUP/PLAY──▶ RTSP Proxy ──透传──▶ 真实摄像头
```

### 3.2 快照

```
客户端 ──GetSnapshotUri──▶ Media Service(返回 http://ip:soapPort/onvif/snapshot?token=<profile>)
客户端 ──GET /onvif/snapshot──▶ ffmpeg -i <真实rtsp> -frames:v 1 ──▶ JPEG
```

快照结果带短 TTL 内存缓存(默认 10s),避免客户端轮询导致 ffmpeg 风暴。

### 3.3 配置热重载

```
UI 保存 ──PUT /api/config──▶ 校验(YAML 语法 + 端口冲突 + 字段合法性)
  ├─ 校验失败 → 400 + 错误详情,不落盘
  └─ 校验成功 → 原子写 config.yaml → Manager.Reload()
                  └─ 停旧 Virtual Devices → 起新 Virtual Devices → WS-Discovery 更新设备表(Bye/Hello)
```

### 3.4 MCP 工具调用

```
AI 客户端(如 Claude Code)──POST /mcp(JSON-RPC)──▶ go-sdk(Streamable HTTP,无状态)
  └─ 路由到 12 个工具之一(list_devices/add_device/probe_rtsp/take_snapshot/run_onvif_selftest 等)
       └─ 复用 REST API 同款后端逻辑(Backend/Config Store)──▶ 结果(text/image 块)──▶ AI 客户端
```

详细工具清单、认证与错误语义见 docs/07-mcp.zh-CN.md。

## 4. 目录结构(规划)

```
onvif-proxy/
├── cmd/onvif-proxy/main.go        # 入口:参数解析、组装、信号处理
├── internal/
│   ├── config/                    # YAML 模型、加载/校验/原子写、默认值生成(UUID/MAC)
│   │   ├── env.go                 # ApplyEnvOverrides:ONVIF_* 环境变量运行时覆盖(仅内存生效,见 docs/03 §3)
│   │   └── env_test.go
│   ├── soap/                      # Envelope 解析、WSSE 验证、Fault/响应渲染
│   ├── onvif/
│   │   ├── device.go              # Device Service handlers
│   │   ├── media.go               # Media Service handlers
│   │   ├── templates/             # XML 响应模板(text/template)
│   │   └── server.go              # 每设备 HTTP server、路由、快照端点
│   ├── discovery/                 # WS-Discovery UDP 监听与报文
│   ├── rtspproxy/                 # TCP 透传代理
│   ├── rtsp/                      # 原生 RTSP 探测客户端(OPTIONS/DESCRIBE、Digest、SDP 解析)
│   ├── mediautil/                 # ffmpeg 快照 / MJPEG 预览封装
│   └── web/
│       ├── web.go / devices.go …  # REST API
│       ├── mcp.go                 # /mcp 端点:注册 12 个 MCP 工具,复用 REST 后端逻辑(见 docs/07-mcp.zh-CN.md)
│       ├── mcp_test.go
│       ├── onvif_selftest.go      # ONVIF 自检:对本机虚拟设备逐方法调用并比对基准表
│       ├── test.go                # RTSP 探测/连通性测试等 /api/test/* 接口
│       ├── ui/                     # Preact+TSX 源码(package.json / tsconfig / src),esbuild 构建
│       └── static/                # go:embed 目标:index.html 薄壳 + dist/(提交的构建产物)
├── docs/                          # 本设计文档
├── config.example.yaml
├── Dockerfile
├── compose.yaml
└── README.md
```

## 5. 技术选型与理由

| 决策 | 选择 | 理由 |
|------|------|------|
| SOAP 实现 | **手写 XML 模板**,不用 WSDL 代码生成 | ONVIF WSDL 生成的 Go 代码庞大且难控;上游项目的 500 问题正源于 soap 库对未注册方法的粗暴处理。手写模板可逐字节对照真实摄像头报文,且 Fault 语义完全可控 |
| XML 解析 | 标准库 `encoding/xml`(token 流式) | 只需提取 Body 首个子元素名 + 少量参数 + WSSE 头,无需完整反序列化 |
| 配置 | `gopkg.in/yaml.v3` | 核心路径唯一第三方依赖;支持注释保留场景可后续评估 |
| MCP 端点 | 官方 `github.com/modelcontextprotocol/go-sdk` | 依赖最小化约束的唯一例外(见 docs/07):MCP 协议面(生命周期/会话/SSE/版本协商)由官方库长期跟进,自造收益低;仅 `internal/web` 的 `/mcp` 路由使用 |
| UUID/MAC | `crypto/rand` 自生成(RFC 4122 v4 / 本地管理 MAC) | 免依赖;首次生成后写回 YAML 持久化,保证客户端眼中设备身份稳定 |
| 快照/预览 | 外部 ffmpeg 进程 | 拉流解码自实现成本过高;ffmpeg 在 Docker 镜像内置,宿主机跑则要求 PATH 中存在 |
| RTSP 探测 | **原生实现**(不走 ffmpeg) | UI"测试连接"需要精确区分错误类别(TCP 不通 / 401 认证失败 / 404 路径错 / SDP 无视频轨),ffmpeg 的报错不可编程解析 |
| Web UI | **Preact + TSX + esbuild**,`go:embed` 打包 | 组件化 + TypeScript 类型安全(设备卡片/表单/配置编辑器可维护性优于单文件原生 JS);Preact 运行时仅 ~6KB。构建产物(`internal/web/static/dist/{app.js,app.css}`)**提交进仓库**,`go:embed all:static` 打进二进制,运行时/Docker 镜像**零 Node 依赖**,单二进制不变。源码在 `internal/web/ui/`(`npm run build` 由 esbuild 打包,CI 校验 dist 未漂移) |

## 6. 并发与生命周期

- 每个 Virtual Device 的 HTTP server、RTSP 代理各自独立 goroutine;`Manager` 用 `context` 树统一取消。
- 热重载语义:**stop-then-start**(先全停再全起)。设备数量少(个位数~几十),中断 < 1s,换取实现简单、无端口占用竞态。
- ffmpeg 子进程与 HTTP 请求生命周期绑定:客户端断开 → context 取消 → 进程组 kill,不留僵尸。
- 优雅退出:SIGTERM → WS-Discovery 发 Bye → 关闭全部 listener → 等待在途请求(超时 5s)。

## 7. 非目标(明确不做)

- PTZ、音频回传、Events/门铃、ONVIF Profile T/G/M —— Profile S 直播 + 快照即满足 Unifi Protect 收编需求。
- RTSP 重新封装/转码 —— 客户端拿到的流就是真实摄像头的流,H.265 是否可用取决于客户端本身。
- 多节点/集群 —— 单进程单配置文件。
