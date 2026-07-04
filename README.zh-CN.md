# onvif-proxy

[English](README.md) | **简体中文**

用 Go 编写的 **RTSP → ONVIF 虚拟摄像机代理**。把任意已有的 RTSP 流(IP 摄像头、NVR 通道、树莓派、ffmpeg 推流等)包装成符合 ONVIF Profile S 规范的虚拟设备,供 Unifi Protect、群晖 Surveillance Station、Frigate 等 ONVIF 客户端自动发现和收编。

> **为什么再造一个轮子?** 现有的 [daniela-hase/onvif-server](https://github.com/daniela-hase/onvif-server) 及其 fork [p10tyr/rtsp-to-onvif](https://github.com/p10tyr/rtsp-to-onvif) 基于 Node.js `soap` 库 + WSDL 实现,凡未显式注册的方法都会抛出格式不规范的裸 HTTP 500(实测 `GetCapabilities`、`GetScopes`、`GetNetworkInterfaces` 均会 500),导致部分客户端判定设备不兼容。本项目用 Go 从零重写,完整实现必选方法集与标准 Fault 语义,并内置 Web UI 用于配置和测试。

## 特性

- **ONVIF Profile S 必选方法全覆盖** —— Device 与 Media 服务的必选方法全部实现;未实现的可选方法返回规范的 `ter:ActionNotSupported` SOAP Fault,而非裸 500。
- **每设备多媒体 Profile** —— 不限于高/低两档:可定义任意数量的命名流(`main` / `sub` / `mobile` / …),每条流对应一个独立的 ONVIF Profile、编码配置与流地址。
- **快照支持** —— `GetSnapshotUri` 开箱即用:真实摄像头自带 HTTP 快照接口则直接透传,没有则由内置 ffmpeg 从 RTSP 流抓取 JPEG 帧(带短 TTL 缓存)。补上了上游项目缺失的能力。
- **WS-Discovery** —— UDP 3702 组播发现,Hello / Bye / ProbeMatches 齐全,客户端可自动发现虚拟设备。
- **零转码 RTSP 代理** —— 每设备一个 TCP 透传代理,字节原样转发到真实摄像头;不解码不转码,无 CPU 开销。ffmpeg 仅用于快照与 UI 预览。
- **Web UI** —— 除 YAML 配置文件外,内嵌小型 Web 后端:在线编辑配置、RTSP 连通性探测(原生 RTSP 客户端,支持 Digest/Basic 认证与 SDP 解析)、抓取快照、MJPEG 实时预览、对虚拟设备本身做 ONVIF 自检。
- **Docker 优先部署** —— 多阶段构建,镜像内置 ffmpeg;提供 macvlan compose 示例,每台虚拟设备可在局域网拿到独立 IP/MAC。
- **单二进制、极少依赖** —— 仅依赖 `gopkg.in/yaml.v3`;SOAP 报文为手写 XML 模板,不用 WSDL 代码生成。

## 文档

| 文档 | 内容 |
|------|------|
| [docs/01-architecture.md](docs/01-architecture.md) | 总体架构、模块划分、数据流、目录结构 |
| [docs/02-onvif-spec.md](docs/02-onvif-spec.md) | ONVIF / SOAP / WS-Discovery / WSSE 规范符合性设计、方法矩阵、Fault 语义 |
| [docs/03-config.md](docs/03-config.md) | YAML 配置格式与校验规则 |
| [docs/04-web-api.md](docs/04-web-api.md) | Web 后端 REST API 与 UI 设计 |
| [docs/05-deployment.md](docs/05-deployment.md) | Docker / macvlan 部署方案 |

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
