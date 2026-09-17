ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}golang:1.25.11 AS build-base
WORKDIR /app

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

COPY backend/core/go.mod backend/core/go.sum ./
RUN go mod download

COPY backend/core/ ./

FROM build-base AS build
RUN CGO_ENABLED=0 go build -o /core .

FROM build-base AS skill-bundler-build
RUN CGO_ENABLED=0 go build -o /builtin-skill-bundle ./cmd/builtin-skill-bundle

FROM build-base AS feishu-cli-sidecar-build
RUN CGO_ENABLED=0 go build -o /feishu-cli-sidecar ./cmd/feishu-cli-sidecar

FROM ${DOCKER_MIRROR}alpine:3.19 AS skill-bundler
ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates
COPY --from=skill-bundler-build /builtin-skill-bundle /usr/local/bin/builtin-skill-bundle
ENTRYPOINT ["/usr/local/bin/builtin-skill-bundle"]

FROM ${DOCKER_MIRROR}alpine:3.19 AS runtime
ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates wget tzdata git
WORKDIR /app
COPY --from=build /core /core
# SQL migrations (read by MIGRATIONS_DIR, default /migrations in docker-compose)
COPY --from=build /app/migrations /migrations
# Model catalog YAML (seeded at startup from ./config/model_catalog.yaml)
COPY --from=build /app/config /app/config
# Built-in Workflow packages are imported into Core's revision store at startup.
COPY workflows /app/workflows
EXPOSE 8000
CMD ["/core"]

FROM ${DOCKER_MIRROR}alpine:3.19 AS feishu-cli-sidecar
ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
ARG TARGETARCH
COPY backend/core/providerconnection/feishu-cli-release.json /tmp/feishu-cli-release.json
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates wget tar jq \
    && mkdir -p /tmp/lark-cli \
    && version="$(jq -er '.version' /tmp/feishu-cli-release.json)" \
    && expected="$(jq -er --arg platform "linux-${TARGETARCH}" '.archive_sha256[$platform]' /tmp/feishu-cli-release.json)" \
    && license_sha256="$(jq -er '.license_sha256' /tmp/feishu-cli-release.json)" \
    && wget -q "https://github.com/larksuite/cli/releases/download/v${version}/lark-cli-${version}-linux-${TARGETARCH}.tar.gz" -O /tmp/lark-cli.tar.gz \
    && echo "${expected}  /tmp/lark-cli.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/lark-cli.tar.gz -C /tmp/lark-cli \
    && install -m 0755 "$(find /tmp/lark-cli -type f -name lark-cli -print -quit)" /usr/local/bin/lark-cli \
    && sha256sum /usr/local/bin/lark-cli | cut -d ' ' -f 1 > /usr/local/share/lark-cli.sha256 \
    && mkdir -p /usr/local/share/licenses/lark-cli \
    && wget -q "https://raw.githubusercontent.com/larksuite/cli/v${version}/LICENSE" -O /usr/local/share/licenses/lark-cli/LICENSE \
    && echo "${license_sha256}  /usr/local/share/licenses/lark-cli/LICENSE" | sha256sum -c - \
    && rm -rf /tmp/lark-cli /tmp/lark-cli.tar.gz /tmp/feishu-cli-release.json \
    && apk del jq
COPY --from=feishu-cli-sidecar-build /feishu-cli-sidecar /usr/local/bin/feishu-cli-sidecar
ENV LAZYMIND_FEISHU_CLI_PATH=/usr/local/bin/lark-cli \
    LAZYMIND_FEISHU_CLI_SHA256_FILE=/usr/local/share/lark-cli.sha256 \
    LAZYMIND_FEISHU_CLI_RUNTIME_ROOT=/var/lib/lazymind/feishu-cli \
    LAZYMIND_FEISHU_CLI_SIDECAR_LISTEN=0.0.0.0:19091
EXPOSE 19091
CMD ["/usr/local/bin/feishu-cli-sidecar"]

FROM runtime AS final
