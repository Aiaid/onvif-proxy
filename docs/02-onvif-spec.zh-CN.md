# 02 · ONVIF 规范符合性设计

[English](02-onvif-spec.md) | **简体中文**

本项目"按规范做完善"的具体落点。引用的规范:

| 规范 | 用途 |
|------|------|
| ONVIF Core Specification | Device 服务、发现、安全、Fault 语义 |
| ONVIF Media Service Specification (ver10) | Media 服务、Profile/StreamUri/SnapshotUri |
| ONVIF Profile S | 必选功能集的判定基准 |
| W3C SOAP 1.2 | Envelope 结构、Fault 结构、HTTP 状态码映射 |
| WS-Discovery (2005/04 draft,ONVIF 指定版本) | UDP 3702 组播发现 |
| OASIS WS-Security UsernameToken Profile 1.0 | WSSE PasswordDigest 认证 |
| RFC 2326 (RTSP 1.0) / RFC 7826 (RTSP 2.0) | RTSP 探测客户端(实现按 1.0,兼容绝大多数摄像头) |
| RFC 7616 / RFC 2617 (HTTP Digest) | RTSP 探测的 401 认证 |
| RFC 4566 (SDP) | DESCRIBE 结果解析 |
| RFC 4122 (UUID) | 设备 EndpointReference 地址 |

## 1. SOAP 层规范

### 1.1 请求处理

- 仅接受 `POST`;`Content-Type` 接受 `application/soap+xml` 与 `text/xml`(兼容 SOAP 1.1 客户端习惯,报文按 1.2 处理)。
- 方法路由依据 **Body 首个子元素的 localName + namespace**,不依赖 SOAPAction/WS-Addressing Action 头(部分客户端不发)。
- 用 `encoding/xml` token 流提取,不做全量反序列化;参数(如 `ProfileToken`)按需提取。

### 1.2 响应

- 统一 `Content-Type: application/soap+xml; charset=utf-8`。
- 响应模板逐条对照 ONVIF WSDL 的 element 定义手写,命名空间前缀固定声明在 Envelope 上(`tds`/`trt`/`tt`/`ter` 等)。
- 所有动态字段经 XML 转义。

### 1.3 Fault 语义(核心差异点)

上游项目的问题:未注册方法 → soap 库抛非规范 500。本项目按 ONVIF Core Spec 的 fault 表实现:

| 场景 | SOAP Code/Subcode | HTTP 状态码 |
|------|-------------------|-------------|
| 方法未实现(可选方法) | `env:Receiver` / `ter:ActionNotSupported` | 500(SOAP 1.2 规定 Receiver→500;**报文体是规范的 Fault XML**,客户端可正确降级) |
| 请求格式非法 / 参数缺失 | `env:Sender` / `ter:InvalidArgVal` | 400 |
| 未认证 / 认证失败 | `env:Sender` / `ter:NotAuthorized` | 400 |
| 未知 Profile token | `env:Sender` / `ter:InvalidArgVal` | 400 |

Fault 报文结构(SOAP 1.2 §5.4):`Code/Subcode/Reason/Detail` 齐全,`Reason` 带 `xml:lang="en"`。

## 2. 认证(WSSE)

- 实现 OASIS UsernameToken Profile 的 **PasswordDigest** 校验:
  `Digest = Base64( SHA-1( Base64Decode(Nonce) + Created + Password ) )`
- `Created` 时间偏差容忍 ±5 分钟(编译期硬编码常量,当前不支持配置修改),防重放的 nonce 缓存暂不做(局域网场景,权衡复杂度)。
- **`GetSystemDateAndTime` 永远免认证**(Core Spec 明确要求:客户端靠它校时后才能算出合法 Digest)。
- 每设备可配置 `auth.username/password`;不配置则**全放行**(接受任意/缺失 WSSE 头)—— 与上游行为一致,便于内网使用。
- PasswordText 模式一并接受(部分客户端只发明文)。

## 3. WS-Discovery

- 单 UDP socket 加入组播 `239.255.255.250:3702`,代答本进程内全部虚拟设备。
- **Hello**:每设备启动/热重载后组播发送;**Bye**:优雅退出/设备移除时发送。
- **Probe → ProbeMatches**:
  - 校验 `d:Types`:为空或包含 `dn:NetworkVideoTransmitter`/`tds:Device` 才应答;
  - 按规范在 `0 ~ APP_MAX_DELAY(500ms)` 随机延迟后单播回复,多设备逐台 ProbeMatch;
  - `RelatesTo` 回填请求 `MessageID`;`XAddrs` 为 `http://<ip>:<port>/onvif/device_service`。
- Scopes(同时用于 `GetScopes`):
  ```
  onvif://www.onvif.org/type/video_encoder
  onvif://www.onvif.org/type/Network_Video_Transmitter
  onvif://www.onvif.org/Profile/Streaming
  onvif://www.onvif.org/name/<设备名>
  onvif://www.onvif.org/hardware/<型号>
  onvif://www.onvif.org/location/
  ```
- 设备 EPR 地址:`urn:uuid:<设备uuid>`,UUID 首次生成后持久化到 YAML,保证客户端重启后仍认得同一台设备。

## 4. Device Service 方法清单

路径 `POST /onvif/device_service`。✅=完整实现,◽=空/固定值响应(合法但无内容),✗=规范 Fault。

| 方法 | 实现 | 说明 |
|------|:---:|------|
| GetSystemDateAndTime | ✅ | UTC + 本地时间,`DateTimeType=NTP`;免认证 |
| SetSystemDateAndTime | ◽ | 接受并返回空响应(虚拟设备无系统时钟可设) |
| GetCapabilities | ✅ | Device/Media 两节;Media 含 `StreamingCapabilities(RTP_TCP, RTP_RTSP_TCP)`、快照;Events/PTZ/Analytics 不声明 |
| GetServices | ✅ | Device + Media 两个 entry,含版本号;`IncludeCapability` 支持 |
| GetServiceCapabilities | ✅ | Network/System/Security 最小合法集 |
| GetDeviceInformation | ✅ | Manufacturer/Model/FirmwareVersion/SerialNumber/HardwareId,来自配置(有默认值) |
| GetScopes | ✅ | 见上节 Scopes 列表(上游 500 的方法之一) |
| SetScopes / AddScopes | ✗ | ActionNotSupported |
| GetNetworkInterfaces | ✅ | 单接口 `eth0`:HwAddress=配置 MAC、IPv4 手动地址+前缀(上游 500 的方法之一) |
| GetNetworkProtocols | ✅ | HTTP(soap端口)+ RTSP(代理端口) |
| GetHostname | ✅ | FromDHCP=false,Name=设备名 slug |
| GetDNS / GetNTP | ◽ | FromDHCP=true 的最小合法响应 |
| GetUsers | ✅ | 配置了 auth → 返回该用户(Administrator);未配置 → 空列表 |
| CreateUsers/DeleteUsers/SetUser | ✗ | ActionNotSupported |
| GetWsdlUrl | ✅ | `http://www.onvif.org/` |
| SystemReboot | ◽ | 返回 `Message: Rebooting...`,实际 no-op |
| GetSystemLog / GetSystemBackup | ✗ | ActionNotSupported |
| 其余未列方法 | ✗ | ActionNotSupported(规范 Fault) |

## 5. Media Service 方法清单

路径 `POST /onvif/media_service`(`GetCapabilities`/`GetServices` 中如实通告)。

数据模型(**多 Profile**,数量由配置的 `streams[]` 决定,N ≥ 1):每设备 1 个 VideoSource(token `src`)、1 个 VideoSourceConfiguration(token `vsc`,全体 Profile 共享)、每条流 1 个 VideoEncoderConfiguration(token `vec_<name>`)、每条流 1 个 Profile(token `profile_<name>`,`fixed=true`)。ONVIF 对同一 VideoSource 挂任意多个 Profile 是规范允许的;Unifi Protect 会取前两个作为高/低流,其他客户端(Frigate、Home Assistant)可任选 Profile。

| 方法 | 实现 | 说明 |
|------|:---:|------|
| GetServiceCapabilities | ✅ | `SnapshotUri`/`StreamingCapabilities(RTP_TCP, RTP_RTSP_TCP)`/`ProfileCapabilities(MaximumNumberOfProfiles=流数量)` |
| GetProfiles / GetProfile | ✅ | 每条配置流一个 Profile(N 个);含 VSC + VEC 完整节点 |
| CreateProfile / DeleteProfile | ✗ | ActionNotSupported(Profile 固定) |
| GetVideoSources | ✅ | 分辨率/帧率取主流(streams[0])配置 |
| GetVideoSourceConfigurations / ~Configuration | ✅ | Bounds = 完整画面 |
| GetVideoEncoderConfigurations / ~Configuration | ✅ | H264(可配 H265 直通声明),分辨率/帧率/码率/GOP 逐流来自配置 |
| GetVideoEncoderConfigurationOptions | ✅ | 仅列出配置中各流的分辨率档位(虚拟设备不可真正改编码) |
| SetVideoEncoderConfiguration | ◽ | 接受并忽略(返回空成功;客户端收编流程常会调用) |
| GetStreamUri | ✅ | `rtsp://<ip>:<该流代理端口>/<原始path>`;按 ProfileToken 找到对应流 |
| GetSnapshotUri | ✅ | `http://<ip>:<soapPort>/onvif/snapshot?token=<profile>`;后端两种模式:真实摄像头快照 URL 透传,或 ffmpeg 抓帧(带 TTL 缓存),见 03 文档 `snapshot` 节 |
| GetGuaranteedNumberOfVideoEncoderInstances | ✅ | `TotalNumber`/`H264` 均等于配置的流数量 |
| GetAudioSources / GetAudioEncoderConfigurations 等音频类 | ◽ | 空列表(合法响应,声明无音频) |
| GetCompatibleVideoSourceConfigurations / GetCompatibleVideoEncoderConfigurations 等 Compatible 类 | ✅ | 与对应 Get~Configuration(s) 共用渲染函数,返回同一固定配置 |
| GetOSDs | ◽ | 空列表 |
| 其余未列方法 | ✗ | ActionNotSupported |

## 6. RTSP 探测客户端(Web UI"测试连接")

原生实现(不依赖 ffmpeg),按 RFC 2326:

1. TCP 连接(超时 5s)→ `OPTIONS`(带 `CSeq`、`User-Agent`);
2. `DESCRIBE`(`Accept: application/sdp`);401 时按 `WWW-Authenticate` 依次支持 **Digest(RFC 7616,MD5)** 与 **Basic**;
3. 解析 SDP(RFC 4566):`m=video/audio` 轨道、`a=rtpmap`(编码名 H264/H265/…)、`a=fmtp` 中 H264 `sprop-parameter-sets` 可解出 profile-level、`a=framerate`/`a=framesize`(如有);
4. 输出结构化结果:`连通性 / 认证结果 / 状态码 / 编码 / 轨道数 / server 头`,UI 按类别显示错误(TCP 不通 ≠ 密码错 ≠ 路径 404)。

## 7. 与上游对照的验收基准

实现完成后,自检(Web UI"ONVIF 自检"按钮,详见 04 文档)必须全绿:

| 方法 | p10tyr 镜像 | daniela-hase 上游 | 本项目目标 |
|------|:---:|:---:|:---:|
| GetCapabilities | ❌ 500 | ✅ | ✅ 200 |
| GetServices | ✅ | ✅ | ✅ 200 |
| GetSystemDateAndTime | ✅ | ✅ | ✅ 200(免认证) |
| GetScopes | ❌ 500 | ❌ | ✅ 200 |
| GetNetworkInterfaces | ❌ 500 | ❌ | ✅ 200 |
| GetDeviceInformation | — | ✅ | ✅ 200 |
| GetProfiles / GetStreamUri / GetSnapshotUri | — | 部分 | ✅ 200 |
| 任意未实现方法 | 裸 500 | 裸 500 | 规范 ActionNotSupported Fault |
