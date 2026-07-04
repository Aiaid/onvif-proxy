# 03 · 配置文件格式

配置文件为 YAML,默认路径 `./config.yaml`(可用 `-config` 参数或 `CONFIG` 环境变量覆盖)。Web UI 的修改经校验后**原子写回**同一文件;程序自动生成的持久化字段(uuid、mac)也会写回。少量全局字段支持环境变量运行时覆盖(见第 3 节),便于 Docker 部署时免挂载改配置。

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

    # 流列表 → 每条流一个 ONVIF Profile(数量不限,至少 1 条)。
    # 第一条为主流:VideoSource 分辨率、默认快照源都取自它。
    streams:
      - name: main               # profile token = profile_main;每设备内唯一
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/main/av_stream"
        width: 2560
        height: 1440
        framerate: 25
        bitrate: 4096            # kbps,仅用于能力通告
      - name: sub
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/sub/av_stream"
        width: 640
        height: 360
        framerate: 15
        bitrate: 512
      - name: mobile             # 第三条、第四条…随意加
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/mobile/av_stream"
        width: 352
        height: 288
        framerate: 10
        bitrate: 128

    snapshot:
      # 可选。真实摄像头自带 HTTP 快照时填其 URL,代理直接转发(透传模式);
      # 留空则用 ffmpeg 从流中抓帧(默认取第一条流,可用 stream 指定)。
      url: ""
      stream: ""                 # ffmpeg 抓帧用哪条流,默认 streams[0](主流清晰度最高,但抓帧稍慢;指定 sub 更快)
      cache_seconds: 10

  - name: "门口 NVR 通道 3"
    ports: { soap: 8082, rtsp: 8555 }
    streams:
      - name: main
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
| `streams[]` | ✅ | 至少 1 条;每条 → 一个 ONVIF Profile。`name` 设备内唯一(小写字母数字/下划线,进入 token:`profile_<name>` / `vec_<name>`);第一条为主流 |
| `streams[].rtsp` | ✅ | 必须为 `rtsp://` scheme;可含 userinfo;host、path 解析后用于代理目标与 StreamUri 路径 |
| `streams[].width/height/framerate/bitrate` | ✅ | 正整数;仅用于 ONVIF 能力通告,不影响真实流 |
| `streams[].proxy_port` | — | 该流上游 host:port 与主流不同时**必填**(独立代理监听);相同则省略,共用 `ports.rtsp` |
| `auth` | — | 提供则 username/password 均非空。**与 RTSP 凭证是独立的两层**:auth 保护虚拟设备的 ONVIF 接口(WSSE),RTSP 认证由客户端与真实摄像头端到端完成。省略 = 全放行(推荐);要配则建议与摄像头 RTSP 账密一致(Unifi Protect 只填一组账密并同时用于两层) |
| `snapshot.url` | — | 填了走透传模式;`http(s)://` scheme |
| `snapshot.stream` | — | ffmpeg 抓帧模式下的取帧流名,默认 `streams[0].name` |

### RTSP URL 的拆解逻辑

`rtsp://user:pass@host:port/path?query` 拆为:

- **代理目标**:`host:port`(TCP 透传的对端);
- **StreamUri 返回值**:`rtsp://<advertise>:<代理端口>/path?query` —— **凭证不出现在 StreamUri 中**,RTSP 认证由客户端与真实摄像头端到端完成(客户端里录入的摄像头凭证 = 真实摄像头凭证);
- **多流的代理端口分配**:与主流同 `host:port` 的流共用 `ports.rtsp`;上游不同的流必须显式给 `proxy_port`(每个独立上游一个监听端口)。校验器检查所有 (soap/rtsp/proxy_port/web) 端口全局不冲突。

### 端口规划建议

| 用途 | 建议区间 |
|------|----------|
| web | 8080 |
| 设备 soap | 8081、8082、… |
| 设备 rtsp | 8554、8555、… |

## 3. 环境变量覆盖

面向 Docker/compose 场景:`server` 与 `web` 两段的全局字段可用环境变量覆盖,不必往挂载卷里预写敏感信息。

| 环境变量 | 覆盖字段 | 取值 |
|------|------|------|
| `CONFIG` | 配置文件路径(等价 `-config` 参数) | 路径 |
| `ONVIF_ADVERTISE_IP` | `server.advertise_ip` | IP 字符串 |
| `ONVIF_DISCOVERY` | `server.discovery` | `true` / `false` |
| `ONVIF_WEB_ENABLED` | `web.enabled` | `true` / `false` |
| `ONVIF_WEB_PORT` | `web.port` | 1-65535 |
| `ONVIF_WEB_USERNAME` | `web.username` | 字符串 |
| `ONVIF_WEB_PASSWORD` | `web.password` | 字符串 |

语义:

- **优先级 env > yaml**;仅在进程内存中生效,**绝不写回** config.yaml——Web UI 的设备增删改、配置整文保存都从文件原文出发,env 值不会被固化进挂载的配置文件;
- 未设置或值为空串 = 不覆盖(沿用 yaml 值/默认值);
- 覆盖后重新执行完整校验(端口全局不冲突、username/password 必须成对),取值非法或校验失败则**启动失败**并打印原因;
- Basic 认证的启用条件不变:生效的 `username` 非空才启用。因此 yaml 中 username 为空时只设 `ONVIF_WEB_PASSWORD` 会因成对约束校验失败,需同时给 `ONVIF_WEB_USERNAME`;
- 设备列表(`devices`)不支持 env 覆盖——按台配置请用挂载的 config.yaml 或 Web UI。

## 4. 生成配置的途径

1. **零配置启动(已实现)**:config 文件不存在时自动生成一份默认配置(web 开启、devices 为空),然后通过 Web UI 添加设备;
2. **Web UI(已实现)**:"➕ 新增设备"表单 → 填主流/子码流 RTSP URL → "探测"按钮先测连通性再自动回填编码/分辨率/帧率 → 填名称与端口(默认建议下一空闲端口对)、可选 ONVIF 认证 → "添加"经 `POST /api/devices` 合入 YAML 并热重载;设备卡片"删除"按钮经 `DELETE /api/devices/{uuid}` 移除;
3. 手写 YAML(照抄 `config.example.yaml`)。
