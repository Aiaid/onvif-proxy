# 05 · 部署方案(Docker)

## 1. 镜像

多阶段构建,最终镜像 = `alpine` + ffmpeg + 单二进制,目标体积 < 120MB(ffmpeg 占大头)。

```dockerfile
# ---- build ----
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/onvif-proxy ./cmd/onvif-proxy

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ffmpeg tzdata ca-certificates
COPY --from=build /out/onvif-proxy /usr/local/bin/onvif-proxy
VOLUME /config
EXPOSE 8080
ENTRYPOINT ["onvif-proxy", "-config", "/config/config.yaml"]
```

多架构:`linux/amd64` + `linux/arm64`(树莓派/NAS),用 `docker buildx` + GitHub Actions 发布到 GHCR。

### 环境变量

全局字段支持 env 覆盖(env > yaml,仅内存生效、不写回挂载的 config.yaml;完整语义见 `docs/03-config.md` 第 3 节):`ONVIF_ADVERTISE_IP`、`ONVIF_DISCOVERY`、`ONVIF_WEB_ENABLED`、`ONVIF_WEB_PORT`、`ONVIF_WEB_USERNAME`、`ONVIF_WEB_PASSWORD`,以及 `CONFIG`(配置文件路径)。典型用途:compose 里直接给 Web UI 配 Basic 认证、bridge 模式下指定宿主机 IP,而不必预先手写 config.yaml。

## 2. 网络模式选择(关键)

WS-Discovery 依赖 UDP 组播,普通 bridge 网络出不去。三种模式按推荐排序:

> 仓库根目录自带的 `compose.yaml` 就是下面这份模板的实体化版本(默认启用模式 B / host 网络);macvlan 的 `networks` 顶层块和 bridge 的 `ports` 块都以注释形式整体保留,按需注释/取消注释相应段落切换模式,不必另外新建文件。

### 模式 A:macvlan(推荐,发现 + 多设备最干净)

容器拿宿主机所在网段的独立 IP/MAC,组播、发现、收编全部原生工作;Unifi Protect 看到的就是"网络上多了一台摄像机"。

```yaml
# compose.yaml
services:
  onvif-proxy:
    image: ghcr.io/aiaid/onvif-proxy:latest
    restart: unless-stopped
    networks:
      lan:
        ipv4_address: 192.168.1.99      # 与 config.yaml 的 advertise_ip 一致
    volumes:
      - ./config:/config

networks:
  lan:
    driver: macvlan
    driver_opts:
      parent: eth0                       # 宿主机物理网卡
    ipam:
      config:
        - subnet: 192.168.1.0/24
          gateway: 192.168.1.1
          ip_range: 192.168.1.96/28      # 预留给容器的小段
```

注意:macvlan 下**宿主机自身默认无法访问容器 IP**(内核限制),管理 UI 从局域网其他机器访问,或另加 macvlan shim 接口。

### 模式 B:host 网络(Linux 宿主机,单容器最简单)

```yaml
services:
  onvif-proxy:
    image: ghcr.io/aiaid/onvif-proxy:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - ./config:/config
    environment:                         # 可选:env 覆盖全局配置(见 docs/03 §3)
      ONVIF_WEB_USERNAME: admin
      ONVIF_WEB_PASSWORD: ${WEB_PASSWORD}
```

组播可用;所有端口直接占宿主机,注意规划避免冲突。compose.yaml 里同一位置还留了一份被注释的 `ONVIF_WEB_PORT` 覆盖示例(改 Web UI 监听端口),按需取消注释即可,无需改 config.yaml。

### 模式 C:bridge + 手动添加(发现不可用)

端口逐个映射(8080、每设备的 soap/rtsp 端口),`advertise_ip` 填宿主机 IP,`discovery: false`。客户端侧**手动按 IP:端口添加 ONVIF 设备**(Unifi Protect 支持手动录入)。适合 Mac/Windows 的 Docker Desktop(其虚拟化层本就过不了组播)。

## 3. 多虚拟设备与 Unifi Protect

- 同一容器/同一 IP 上开多台虚拟设备(不同 soap 端口)是规范允许的,Protect 按 XAddr(host:port)区分设备,实测可分别收编;
- 若客户端固执地"一 IP 一设备",再用多个 macvlan IP 各跑一个容器实例(每实例一份精简 config)。

## 4. 升级与数据

- 唯一持久化数据 = `/config/config.yaml`(含自动生成的 uuid/mac);
- 升级 = 换镜像重启;uuid/mac 持久化保证客户端不会把设备认成新机器;
- 备份 = 备份该 YAML 一个文件。

## 5. CI/CD(GitHub Actions,已实现:`.github/workflows/docker-publish.yml`)

- **PR** → `go vet` + `go test -race`(只测不构建);
- **push main / tag v\* / 手动** → test → amd64 + arm64 **原生 runner** 并行构建(push-by-digest 到 GHCR,gha 缓存)→ merge job 合并 manifest list;
- 镜像目标:`ghcr.io/aiaid/onvif-proxy`(`GITHUB_TOKEN`,永远可用);仓库配置了 `DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` secrets 时,同一组 tag 自动镜像到 Docker Hub `<user>/onvif-proxy`(imagetools 跨仓库复制 blob,无需二次构建);
- tag 规则:分支名、`v*` 版本(+semver)、短 sha、main 上的 `latest`;`VERSION` build-arg 注入二进制(tag 名或短 sha)。

## 6. 裸机运行(不走 Docker)

```bash
go build -o onvif-proxy ./cmd/onvif-proxy
./onvif-proxy -config config.yaml      # 需 PATH 中有 ffmpeg(仅快照/预览用)
```

macOS 上开发调试完全可行(组播发现在同一局域网内可用);生产建议 Linux。
