# 04 · Web 后端 API 与 UI 设计

Web 服务默认监听 `:8080`,提供 REST API 与内嵌单页 UI。可选 HTTP Basic 认证(`web.username/password`)。

## 1. REST API

统一约定:JSON 响应;错误格式 `{"error": "...", "detail": "..."}`;写操作仅 `PUT/POST`。

### 1.1 配置管理

| 方法/路径 | 说明 |
|-----------|------|
| `GET /api/config` | 返回当前 config.yaml 原文(`text/plain`),UI 编辑器直接展示 |
| `PUT /api/config` | Body = 完整 YAML 文本。流程:解析 → 校验(语法/端口冲突/字段)→ 原子落盘 → 热重载。校验失败返回 400 + 逐条错误,**不落盘** |
| `GET /api/devices` | 结构化设备列表 + 运行时状态:`{name, uuid, soap_port, rtsp_port, running, auth_user, endpoints:{device_service, snapshot, streams:[{name, profile_token, rtsp_uri, rtsp, width, height, framerate, bitrate}]}}`(streams 数组与配置的多 Profile 一一对应)。`rtsp_port`/`auth_user` 与每个 stream 的 `rtsp`(上游源 URL,可能含凭证)、`width/height/framerate/bitrate` 是为**编辑表单预填**而补充的字段:源 URL 凭证的暴露口径与 `GET /api/config`(原样返回全文)一致,均由 web Basic 认证把关;ONVIF 密码**不**下发(见下方 PUT 的密码保留语义) |
| `POST /api/devices` | Body JSON DeviceSpec `{name, soap_port, rtsp_port, auth?, streams:[{name,rtsp,width,height,framerate,bitrate}], snapshot?}`。合入当前 YAML → 校验 → 落盘 → 热重载;uuid/mac/serial 交由加载时生成。校验失败(含端口冲突)返回 400,成功 `{"status":"applied"}` |
| `PUT /api/devices/{uuid}` | Body 与 POST 相同的 DeviceSpec。按 uuid 定位设备(未知 404)→ 用表单值构建新 Device,**原位替换**,但**保留原 uuid/mac/serial 与 info**(表单不携带这些)→ 校验 → 落盘 → 热重载。成功 `{"status":"applied"}`,校验/端口冲突 400 透传。**ONVIF 密码保留语义**:DTO 只暴露 `auth_user` 不暴露密码,故当 body 携带 `auth.username` 但 `auth.password` 为空时视为"保持原密码不变";清空 username(不带 auth 对象)则移除 ONVIF 认证 |
| `DELETE /api/devices/{uuid}` | 按 uuid 从当前配置删除设备 → 校验 → 落盘 → 热重载。未知 uuid 返回 404,成功 `{"status":"applied"}` |

### 1.2 测试工具(对应 UI 测试面板)

| 方法/路径 | 说明 |
|-----------|------|
| `POST /api/test/rtsp` | Body `{"url": "rtsp://..."}`。原生 RTSP 探测(OPTIONS + DESCRIBE + Digest/Basic 认证),返回:`{"ok":true, "status":200, "auth":"digest", "server":"...", "tracks":[{"type":"video","codec":"H264","fmtp":"..."}], "latency_ms": 43}`。错误分类:`dial_timeout` / `auth_failed` / `not_found` / `no_video_track` / `protocol_error` |
| `POST /api/test/streaminfo` | Body `{"url": "rtsp://..."}`。ffprobe 探测首个视频流,返回 `{"codec":"h264","width":1920,"height":1080,"fps":25}`,供新增设备表单回填宽/高/帧率。ffmpeg 不可用返回 501 |
| `GET /api/test/snapshot?device=<uuid>&stream=<name>` | 调 ffmpeg 从指定流抓一帧(`stream` 省略取主流),返回 `image/jpeg`。失败返回 JSON 错误(含 ffmpeg stderr 摘要) |
| `GET /api/preview?device=<uuid>&stream=<name>` | **MJPEG 实时预览**:ffmpeg 拉指定流 → `-f mpjpeg`(缩至 640 宽、5fps、静音)→ `multipart/x-mixed-replace` 推给浏览器,`<img>` 直接播。客户端断开即杀 ffmpeg;每设备并发预览数上限 2 |
| `POST /api/test/onvif?device=<uuid>` | **ONVIF 自检**:服务端以 ONVIF 客户端身份调用自己的 SOAP 端点,逐方法返回 `{method, http_status, soap_fault, pass}`。覆盖方法:GetSystemDateAndTime、GetCapabilities、GetServices、GetScopes、GetNetworkInterfaces、GetDeviceInformation、GetProfiles、GetStreamUri、GetSnapshotUri + 一个故意不存在的方法(验证 Fault 规范性) |
| `GET /api/discovery/log` | 最近 50 条 WS-Discovery 交互(谁在 Probe、回了什么),排查"客户端发现不了设备"用 |

### 1.3 系统

| 方法/路径 | 说明 |
|-----------|------|
| `GET /api/status` | 版本、启动时长、ffmpeg 是否可用、advertise_ip 探测结果 |
| `GET /healthz` | liveness,200 即可 |

## 2. UI 页面设计(单页,三个区块)

**技术栈**:Preact + TSX,由 esbuild 打包成 `static/dist/{app.js,app.css}`(提交进仓库,`go:embed` 进二进制,运行时零 Node)。源码在 `internal/web/ui/`,`index.html` 为薄壳(`<div id="app">` + `/dist/app.js`)。开发:`cd internal/web/ui && npm install && npm run check && npm run build`。

**健壮性约定(全局)**:所有异步操作统一走 `useAsync` + `AsyncButton` 机制 —— 按钮点击后**立即禁用并显示转圈文案**,请求完成/失败后恢复;`useAsync` 内部有再入守卫,天然防双击/防表单重复提交;删除类操作 `confirm()` 通过后按钮即锁。`fetch` 统一封装(`api.ts`):20s 超时(AbortController)、非 2xx 抛 `ApiError`、解析 `{error,detail}` 错误信封。没有裸 `onClick` 异步。

### 2.1 设备列表(首页)

每台设备一张卡片:

```
┌────────────────────────────────────────────────┐
│ 车库摄像头                      ● 运行中        │
│ ONVIF: http://192.168.1.10:8081/onvif/device_service
│ RTSP:  rtsp://192.168.1.10:8554/h264/ch1/main…  │
│ [测试连接] [快照] [预览] [ONVIF 自检] [编辑] [删除] │
└────────────────────────────────────────────────┘
```

- **测试连接** → `/api/test/rtsp`,卡片内显示结果:连通/认证/编码/延迟,错误按类别给中文提示("TCP 连不通,检查 IP 与端口" / "认证失败,检查用户名密码" / "路径 404");
- **快照** → 卡片内直接显示 JPEG;
- **预览** → 弹层 `<img src=/api/preview…>` 实况,关闭(点遮罩/按钮/Esc)即清空 src 断流杀 ffmpeg;
- **ONVIF 自检** → 渲染方法×状态表格(绿✅/红❌),即本项目立项时那张对比表的自动化版;
- **编辑** → 打开与"新增设备"同一表单组件的**编辑模式**,用 `GET /api/devices` 补充的字段(soap/rtsp 端口、`auth_user`、每流 `rtsp/width/height/framerate/bitrate`)预填,提交走 `PUT /api/devices/{uuid}`;ONVIF 密码框留空即保持原密码;
- **删除** → `confirm` 后 `DELETE /api/devices/{uuid}`,成功后刷新设备列表与配置编辑器。

### 2.2 配置编辑

- YAML 全文编辑器(textarea + 等宽字体,不引前端依赖);
- "校验"按钮:dry-run 调 `PUT /api/config?dry_run=1`,只报错不落盘;
- "保存并生效"按钮:落盘 + 热重载,展示重载结果;
- 顶部提示条:配置文件路径、上次保存时间。

### 2.3 新增设备向导(表单)

1. 填 RTSP URL → 点"探测" → 自动带出编码/分辨率(SDP 有则回填,无则手填);
2. 填名称、端口(自动建议下一个空闲端口对)、可选低码流;
3. "添加" → 后端把设备节点合入 YAML → 校验 → 落盘 → 热重载。

## 3. 安全考虑

- Web UI 面向内网管理员;开启 Basic 认证后,`/api/*` 与静态页全部走认证;
- `/api/test/rtsp` 仅接受 `rtsp://` scheme,禁止借道探测任意 TCP 端口之外的协议(SSRF 面收敛:目标端口不限,但协议握手必须是 RTSP);
- 配置文件中的摄像头密码:`GET /api/config` 原样返回(编辑器要能改),因此**开启 web 认证是使用预设密码场景的前提**,文档与 UI 均给出提示;
- ffmpeg 命令行参数全部程序拼装,RTSP URL 经 `exec.Command` 参数传递,无 shell 注入面。
