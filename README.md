# onvif-proxy

用 Go 编写的 **RTSP → ONVIF 虚拟设备代理**。把任意已有的 RTSP 流(IP 摄像头、NVR 通道、树莓派、ffmpeg 推流等)包装成符合 ONVIF Profile S 规范的虚拟摄像头,供 Unifi Protect、Synology Surveillance Station、Frigate 等 ONVIF 客户端发现和收编。

> 动机:现有的 [daniela-hase/onvif-server](https://github.com/daniela-hase/onvif-server) 及其 fork [p10tyr/rtsp-to-onvif](https://github.com/p10tyr/rtsp-to-onvif) 基于 Node.js `soap` 库 + WSDL 实现,凡未注册的方法直接抛出格式不规范的 HTTP 500(实测 `GetCapabilities` / `GetScopes` / `GetNetworkInterfaces` 均会 500),导致部分客户端判定设备不兼容。本项目用 Go 重写,按 ONVIF 官方规范实现完整的必选方法集与标准错误语义,并内置 Web UI 便于配置与测试。

## 特性

- **ONVIF Profile S 必选方法全覆盖**:Device 与 Media 服务的必选方法全部实现;未实现的可选方法返回规范的 `ter:ActionNotSupported` SOAP Fault,而非裸 500。
- **WS-Discovery**:UDP 3702 组播发现,启动 Hello / 退出 Bye / 应答 Probe,客户端可自动发现虚拟设备。
- **RTSP 透传代理**:每个虚拟设备一个 RTSP TCP 代理端口,流量原样转发到真实摄像头,不转码、零性能损耗。
- **快照支持**:内置 ffmpeg 抓帧,实现上游项目缺失的 `GetSnapshotUri`。
- **Web UI**:除 YAML 配置文件外,提供小型 Web 后端 —— 在线编辑配置、测试 RTSP 连接(原生 RTSP 客户端探测)、抓取快照、MJPEG 实时预览、ONVIF 接口自检。
- **纯 Docker 部署**:多阶段构建,镜像内置 ffmpeg;提供 MacVLAN compose 示例(多虚拟设备各拿独立 IP/MAC)。
- **单二进制、极少依赖**:仅依赖 `gopkg.in/yaml.v3`;SOAP 报文手写模板,不依赖 WSDL 代码生成。

## 文档

| 文档 | 内容 |
|------|------|
| [docs/01-architecture.md](docs/01-architecture.md) | 总体架构、模块划分、数据流、目录结构 |
| [docs/02-onvif-spec.md](docs/02-onvif-spec.md) | ONVIF/SOAP/WS-Discovery/WSSE 规范符合性设计,方法清单与错误语义 |
| [docs/03-config.md](docs/03-config.md) | YAML 配置文件格式与字段说明 |
| [docs/04-web-api.md](docs/04-web-api.md) | Web 后端 REST API 与 UI 功能设计 |
| [docs/05-deployment.md](docs/05-deployment.md) | Docker / MacVLAN 部署方案 |

## 快速开始(规划)

```bash
# 1. 准备配置
cp config.example.yaml config.yaml && vim config.yaml

# 2. 启动
docker compose up -d

# 3. 打开 Web UI 验证
open http://<host>:8080
```

## 状态

- [x] 设计文档
- [ ] 核心实现(SOAP 服务、WS-Discovery、RTSP 代理)
- [ ] Web 后端与 UI
- [ ] Docker 镜像与 compose
- [ ] Unifi Protect 实机验证

## License

MIT
