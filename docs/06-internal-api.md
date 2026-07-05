# 06 · 包间接口契约(实现对齐基准)

> 本文档是并行开发各包时的**签名契约**。实现必须精确匹配这里的导出签名;若实现中发现契约不合理,改动前先更新本文档。`internal/config` 已实现,其余包以它为地基。

模块路径:`github.com/Aiaid/onvif-proxy`。依赖规则:所有包可 import `internal/config` 与标准库;**禁止新增第三方依赖**(go.mod 直接依赖仅 yaml.v3,唯一例外:`internal/web` 可 import 官方 `github.com/modelcontextprotocol/go-sdk/mcp` 实现 `/mcp` 端点,契约见 `docs/07-mcp.md`);包之间除下述声明外不得互相 import。

## internal/config(已实现,只读参考)

```go
type Config struct { Server ServerConfig; Web WebConfig; Devices []*Device }
func Load(path string) (*Config, error)          // 读+校验+生成 uuid/mac 并写回
func Parse(data []byte) (*Config, error)         // 严格解析+校验(dry-run 用)
func Save(path string, cfg *Config) error        // 原子写
func ApplyEnvOverrides(cfg *Config) ([]string, error) // ONVIF_* env 覆盖(仅内存,覆盖后重校验);返回已应用项供日志
func DetectAdvertiseIP() string

type Device struct { Name, UUID, MAC, Serial string; Ports Ports; Info Info;
                     Auth *Auth; Streams []*Stream; Snapshot Snapshot }
func (d *Device) PrimaryStream() *Stream
func (d *Device) StreamByName(name string) *Stream
func (d *Device) StreamByProfileToken(token string) *Stream
func (d *Device) ProxyPortFor(s *Stream) int
func (d *Device) ProxyBindings() []ProxyBinding  // {ListenPort int; Target string}
func (d *Device) SnapshotStream() *Stream
func (d *Device) SnapshotCacheSeconds() int

type Stream struct { Name, RTSP string; Width, Height, Framerate, Bitrate, ProxyPort int }
func (s *Stream) TargetAddr() string             // "host:port"(默认 554)
func (s *Stream) PathQuery() string              // "/path?query"
func (s *Stream) SourceURL() string              // 含凭证的完整上游 URL
func (s *Stream) ProfileToken() string           // "profile_<name>"
func (s *Stream) EncoderToken() string           // "vec_<name>"
```

## internal/soap(SOAP 1.2 基础层)

```go
package soap

// 解析后的请求
type Request struct {
    Action    string            // Body 首子元素 localName,如 "GetStreamUri"
    Namespace string            // 该元素的 namespace URI
    Body      []byte            // Body 的 innerxml(供提取参数)
    Security  *UsernameToken    // 无 WSSE 头时为 nil
}
type UsernameToken struct { Username, Password, PasswordType, Nonce, Created string }

func ParseRequest(body []byte) (*Request, error)
// 从 innerxml 提取首个匹配 localName 的元素文本(如 ProfileToken)
func ExtractElement(inner []byte, localName string) (string, bool)

// WSSE PasswordDigest / PasswordText 校验,clockSkew 为 Created 容忍偏差
func (t *UsernameToken) Verify(username, password string, clockSkew time.Duration) bool

// 响应渲染:body 为已渲染的 Body 内部 XML;Envelope/命名空间由本层统一加
func Envelope(body string) []byte

// Fault:写入 w(设置 Content-Type 与 HTTP 状态码)并返回
// code: "Sender"/"Receiver";subcode 如 "ter:ActionNotSupported"
func WriteFault(w http.ResponseWriter, code, subcode, reason string)
// 便捷:
func WriteActionNotSupported(w http.ResponseWriter, action string) // Receiver, HTTP 500
func WriteNotAuthorized(w http.ResponseWriter)                     // Sender, HTTP 400
func WriteInvalidArg(w http.ResponseWriter, reason string)         // Sender, HTTP 400

func XMLEscape(s string) string
```

Envelope 统一声明命名空间前缀:`env`(SOAP 1.2)、`tds`、`trt`、`tt`、`ter`、`wsnt` 不需要。Fault 结构含 Code/Subcode/Reason(xml:lang="en")。

## internal/onvif(Device + Media 服务,每设备一个 HTTP server)

```go
package onvif // 允许 import: internal/config, internal/soap

type Options struct {
    AdvertiseIP  string // 请求无 Host 头时构造 URI 的兜底
    Version      string // firmware 版本展示
    // 快照回调,由 main 注入(透传/ffmpeg/缓存都在 main 侧组合)
    SnapshotFunc func(ctx context.Context, streamName string) (data []byte, contentType string, err error)
}

func NewServer(dev *config.Device, opts Options) *Server
func (s *Server) Run(ctx context.Context) error // 监听 dev.Ports.SOAP,ctx 取消时优雅退出

// 路由:
//   POST /onvif/device_service
//   POST /onvif/media_service
//   GET  /onvif/snapshot?token=<profileToken>   (免 WSSE;调 SnapshotFunc)
```

行为要求(细节见 docs/02):方法矩阵按 02 文档第 4/5 节;`GetSystemDateAndTime` 免认证;`dev.Auth == nil` 全放行;URI 构造优先 `r.Host`;未知方法 → `soap.WriteActionNotSupported`。

## internal/discovery(WS-Discovery)

```go
package discovery

type Device struct { UUID, Name, Hardware, XAddr string } // XAddr 完整 URL
type LogEntry struct { Time time.Time; Remote, Kind, Detail string } // Kind: probe/hello/bye/match

func New(devices []Device) *Server
func (s *Server) Run(ctx context.Context) error  // 绑定 :3702、加组播、Hello;ctx 取消时发 Bye
func (s *Server) SetDevices(devices []Device)    // 热重载:对差集发 Bye/Hello
func (s *Server) Log() []LogEntry                // 最近 50 条,新在前
```

## internal/rtspproxy(TCP 透传)

```go
package rtspproxy

// 监听 127.0.0.1 之外全接口的 listenPort,双向 io.Copy 到 target("host:port")。
// ctx 取消时关闭 listener 与全部在途连接。返回 net.Listen 错误或 ctx.Err()。
func Run(ctx context.Context, listenPort int, target string) error
```

## internal/rtsp(原生 RTSP 探测客户端)

```go
package rtsp

type ErrKind string
const (
    ErrDialTimeout   ErrKind = "dial_timeout"
    ErrAuthFailed    ErrKind = "auth_failed"
    ErrNotFound      ErrKind = "not_found"
    ErrNoVideoTrack  ErrKind = "no_video_track"
    ErrProtocol      ErrKind = "protocol_error"
)
type Track struct { Type, Codec, Fmtp string }     // Type: "video"/"audio"
type Result struct {
    OK        bool
    Status    int           // 最终 RTSP 状态码
    Auth      string        // "none"/"basic"/"digest"
    Server    string        // Server 头
    LatencyMS int64
    Tracks    []Track
    ErrKind   ErrKind       // OK=false 时有效
    ErrDetail string
}
func Probe(ctx context.Context, rawURL string) *Result
// OPTIONS → DESCRIBE;401 时按 WWW-Authenticate 支持 Digest(MD5, RFC 7616)与 Basic;
// SDP 解析 m=/a=rtpmap/a=fmtp;整体超时 5s(可被 ctx 提前取消)
```

## internal/mediautil(ffmpeg 封装)

```go
package mediautil

func Available() bool // PATH 中是否有 ffmpeg

// 抓单帧 JPEG:-rtsp_transport tcp -frames:v 1;stderr 摘要进 error
func Grab(ctx context.Context, rtspURL string) ([]byte, error)

// TTL 缓存 + singleflight(并发同 key 只跑一个 fetch)
type Cache struct{ /* ... */ }
func NewCache(ttl time.Duration) *Cache
func (c *Cache) Get(key string, fetch func() ([]byte, error)) ([]byte, error)

// MJPEG 预览:缩至 maxWidth、fps 帧率、静音,multipart/x-mixed-replace 写入 w;
// 客户端断开(r.Context 取消)即杀 ffmpeg 进程组
func ServeMJPEG(w http.ResponseWriter, r *http.Request, rtspURL string, maxWidth, fps int) error
```

## internal/web(REST API + 内嵌 UI)

```go
package web // 允许 import: internal/config, internal/rtsp, internal/mediautil, internal/discovery

// 由 main 实现
type Backend interface {
    ConfigYAML() ([]byte, error)
    ApplyConfig(raw []byte, dryRun bool) error   // Parse→(落盘+热重载);错误原样返回给前端
    Devices() []DeviceRuntime                    // 运行中的设备快照
    Snapshot(ctx context.Context, dev *config.Device, streamName string) ([]byte, string, error)
    DiscoveryLog() []discovery.LogEntry
    Status() Status
}
type DeviceRuntime struct { Device *config.Device; Running bool }
type Status struct {
    Version, AdvertiseIP string
    UptimeSeconds        int64
    FFmpeg               bool
}

func New(cfg config.WebConfig, backend Backend) *Server
func (s *Server) Run(ctx context.Context) error
```

REST 路由与语义见 docs/04(`/api/config`、`/api/devices`、`/api/test/rtsp`、`/api/test/snapshot`、`/api/preview`、`/api/test/onvif`、`/api/discovery/log`、`/api/status`、`/healthz`)。要点:

- 探测直接调 `rtsp.Probe`;预览直接调 `mediautil.ServeMJPEG`(每设备并发上限 2,web 层管信号量);快照走 `Backend.Snapshot`(缓存/透传由 main 组合);
- **ONVIF 自检**在 web 包内实现:对 `dev.Ports.SOAP` 逐方法发手写 SOAP envelope(WSSE 按 dev.Auth 生成),按 docs/04 的方法清单返回 `[{method, http_status, soap_fault, pass}]`;最后加一个不存在的方法,pass 条件 = 返回体是合法 Fault XML 且 subcode 为 ActionNotSupported;
- 静态 UI:Preact+TSX 源码在 `internal/web/ui/`,esbuild 打包为 `static/dist/{app.js,app.css}`(提交进仓库),`index.html` 薄壳,`go:embed all:static` 进二进制(含 `dist/`);`GET /dist/*` 由 `http.FileServerFS` 从嵌入 FS 服务;`PUT /api/devices/{uuid}` 编辑设备(见 docs/04);`GET /api/devices` DTO 为编辑预填补充了 `rtsp_port/auth_user` 与每流源参数;
- Basic 认证:cfg.Username 非空时对全部路由生效。

## cmd/onvif-proxy(main,集成层,最后实现)

职责:flag 解析(`-config`,默认 `./config.yaml`;`-version`)→ `config.Load` → 组装 Manager(实现 `web.Backend`;每设备起 onvif.Server + rtspproxy;discovery.Server;快照函数 = 透传 or mediautil.Grab + Cache)→ 信号处理与热重载(stop-then-start)。
