FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl tzdata

COPY --chmod=755 komari-${TARGETOS}-${TARGETARCH} /app/komari

# Agent 安装包（install.sh + 各架构二进制 + 校验），由控制器 /download/agent 提供，新装无需 GitHub
COPY agent-dist /app/agent-dist

ENV GIN_MODE=release
ENV KOMARI_LISTEN=0.0.0.0:25774
ENV GODEBUG=disablethp=1

EXPOSE 25774

CMD ["/app/komari", "server"]
