# 03 · 配置文件格式

配置文件为 YAML,默认路径 `./config.yaml`(可用 `-config` 参数或 `CONFIG` 环境变量覆盖)。Web UI 的修改经校验后**原子写回**同一文件;程序自动生成的持久化字段(uuid、mac)也会写回。

## 1. 完整示例

```yaml
# 全局
server:
  # 对外通告用的本机 IP。留空则自动探测(取默认路由出口地址)。
  # Docker bridge 模式必须显式填宿主机 IP;macvlan 模式填容器自己的 IP。
  advertise_ip: ""
  # WS-Discovery 开关(某些环境只想手动添加设备时可关)
  discovery: true

web:
  enabled: true
  port: 8080
  # Web UI 的 HTTP Basic 认证,留空则不启用
  username: ""
  password: ""

# 虚拟设备列表,每台 = 一个 ONVIF 摄像机
devices:
  - name: "车库摄像头"            # 必填;用于 ONVIF 名称 scope、UI 展示
    # ---- 以下三项留空时自动生成并写回,保证设备身份稳定 ----
    uuid: ""                     # RFC 4122 v4
    mac: ""                      # 本地管理地址 (x2-xx-xx-xx-xx-xx)
    serial: ""                   # SerialNumber,默认取 uuid 前 8 位

    ports:
      soap: 8081                 # ONVIF SOAP/HTTP 端口(含快照端点)
      rtsp: 8554                 # RTSP 透传代理端口

    info:                        # GetDeviceInformation 字段,全部可选
      manufacturer: "OnvifProxy"
      model: "VirtualCam"
      firmware: "1.0.0"

    auth:                        # ONVIF WSSE 认证,整块可省(省略 = 全放行)
      username: "admin"
      password: "secret"

    streams:
      high:                      # 必填
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/main/av_stream"
        width: 2560
        height: 1440
        framerate: 25
        bitrate: 4096            # kbps,仅用于能力通告
      low:                       # 可选;省略则只有单 Profile
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/sub/av_stream"
        width: 640
        height: 360
        framerate: 15
        bitrate: 512

    snapshot:
      # 可选。真实摄像头自带 HTTP 快照时填其 URL,代理直接 302/转发;
      # 留空则用 ffmpeg 从 high 流抓帧。
      url: ""
      cache_seconds: 10

  - name: "门口 NVR 通道 3"
    ports: { soap: 8082, rtsp: 8555 }
    streams:
      high:
        rtsp: "rtsp://admin:pw@192.168.1.60:554/cam/realmonitor?channel=3&subtype=0"
        width: 1920
        height: 1080
        framerate: 20
        bitrate: 2048
```

## 2. 字段说明与校验规则

### server

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `advertise_ip` | string | 自动探测 | 写进 XAddrs / StreamUri / SnapshotUri 的 IP。**HTTP 响应内的 URI 优先使用请求的 Host 头**(NAT 友好),此字段主要供 WS-Discovery 使用 |
| `discovery` | bool | true | 是否启动 UDP 3702 |

### devices[]

| 字段 | 必填 | 校验 |
|------|:---:|------|
| `name` | ✅ | 非空;进入 scope 时做 URL 编码 |
| `uuid` / `mac` / `serial` | — | 空则生成后**写回文件**;uuid 须为合法 UUID,mac 须为合法 MAC |
| `ports.soap` / `ports.rtsp` | ✅ | 1-65535;**全体设备 + web 端口两两不冲突**(校验失败拒绝加载/保存) |
| `streams.high.rtsp` | ✅ | 必须为 `rtsp://` scheme;可含 userinfo;host、path 解析后用于代理目标与 StreamUri 路径 |
| `width/height/framerate/bitrate` | ✅(high) | 正整数;仅用于 ONVIF 能力通告,不影响真实流 |
| `streams.low` | — | 同 high;省略则单 Profile |
| `auth` | — | 提供则 username/password 均非空 |

### RTSP URL 的拆解逻辑

`rtsp://user:pass@host:port/path?query` 拆为:

- **代理目标**:`host:port`(TCP 透传的对端);
- **StreamUri 返回值**:`rtsp://<advertise>:<ports.rtsp>/path?query` —— **凭证不出现在 StreamUri 中**,RTSP 认证由客户端与真实摄像头端到端完成(客户端里录入的摄像头凭证 = 真实摄像头凭证);
- 高低两条流的 `host:port` 相同则共用同一个代理端口;不同则拒绝(校验错误,要求拆成两台虚拟设备)。

### 端口规划建议

| 用途 | 建议区间 |
|------|----------|
| web | 8080 |
| 设备 soap | 8081、8082、… |
| 设备 rtsp | 8554、8555、… |

## 3. 生成配置的途径

1. **Web UI**:表单新增设备 → 填 RTSP URL → "测试连接"自动探测编码/分辨率回填 → 保存;
2. **CLI**:`onvif-proxy -create-config` 交互式生成(对齐上游的 `--create-config` 体验);
3. 手写 YAML(照抄 `config.example.yaml`)。
