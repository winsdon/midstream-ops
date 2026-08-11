# =============================================================================
# midstream-ops 多阶段构建
# =============================================================================
# Stage 1: 构建前端（Vue3 + Vite）
# Stage 2: 构建 Go 后端，把前端产物 embed 进单二进制
# Stage 3: 最小运行时镜像
#
# 本地构建：docker build -t midstream-ops:dev .
#   或直接用 deploy/build.ps1（自动识别 docker / podman，已封装好参数）。
#   ⚠️ 用 podman 构建须带 --format docker：默认的 OCI 格式不支持 HEALTHCHECK，
#      会静默丢弃下面的健康检查配置。docker build 无此问题。

# =============================================================================

ARG NODE_IMAGE=node:24-alpine
ARG GOLANG_IMAGE=golang:1.25-alpine
ARG ALPINE_IMAGE=alpine:3.21
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

# -----------------------------------------------------------------------------
# Stage 1: 前端构建
# -----------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS frontend-builder

WORKDIR /app/frontend

# pnpm 固定 v10：匹配 pnpm-lock.yaml 的 lockfileVersion 9.0
RUN corepack enable && corepack prepare pnpm@10 --activate

# 先装依赖，利用层缓存（源码变动不会导致重装）
# pnpm-workspace.yaml 里的 onlyBuiltDependencies 声明了 esbuild 需要跑构建脚本
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

# 复制前端源码并构建。vite.config.ts 的 outDir 指向 ../backend/internal/web/dist，
# 所以产物落在 /app/backend/internal/web/dist
COPY frontend/ ./
RUN pnpm run build

# -----------------------------------------------------------------------------
# Stage 2: 后端构建（embed 前端）
# -----------------------------------------------------------------------------
FROM ${GOLANG_IMAGE} AS backend-builder

ARG GOPROXY
ARG GOSUMDB
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

WORKDIR /app/backend

# 先拉依赖，利用层缓存
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# 先复制后端源码
COPY backend/ ./

# 再复制前端产物 —— 必须在 COPY backend/ 之后。
# 反过来会被后端那次 COPY 整目录覆盖掉（.dockerignore 已排除本地 dist）。
COPY --from=frontend-builder /app/backend/internal/web/dist ./internal/web/dist

# -tags embed 必须带：不带则走 internal/web/embed_off.go，前端完全不服务。
# CGO_ENABLED=0：本项目无 CGO 依赖（PG 驱动 pgx 是纯 Go），可静态编译。
RUN CGO_ENABLED=0 GOOS=linux go build \
    -tags embed \
    -ldflags="-s -w" \
    -trimpath \
    -o /app/monitor \
    ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: 运行时
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE}

LABEL org.opencontainers.image.title="midstream-ops"
LABEL org.opencontainers.image.description="sub2api 上游账号余额 / 成本 / 稳定性监控"
LABEL org.opencontainers.image.licenses="MIT"

# tzdata：二进制已内嵌 time/tzdata，这里装包是为了 TZ 环境变量与容器内 date 显示。
# wget：HEALTHCHECK 用（alpine 的 busybox 自带，此处显式声明依赖意图）。
#
# 不再需要 su-exec：数据全在外部 PG，容器内没有需要修属主的数据目录，
# 因此直接用 USER 降权，不走 entrypoint 脚本。
RUN apk add --no-cache ca-certificates tzdata && \
    rm -rf /var/cache/apk/*

# 非 root 用户
RUN addgroup -g 1000 monitor && \
    adduser -u 1000 -G monitor -s /bin/sh -D monitor

WORKDIR /app

COPY --from=backend-builder --chown=monitor:monitor /app/monitor /app/monitor

USER monitor

EXPOSE 9090

# /health 免鉴权，两个库的语义刻意不对称：
#   • 上游 sub2api 库不可用 → 仍返回 200（upstream_pg:"down"）。上游挂掉不代表
#     本容器不健康，相关接口各自降级返回 503。
#   • 本地 monitor 库不可用 → 返回 503（store_pg:"down"）。它是写路径的唯一
#     依赖，此时服务确实不健康，应让本检查失败并触发重启。
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:9090/health || exit 1

ENTRYPOINT ["/app/monitor"]
