ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}golang:1.25.11 AS build
WORKDIR /app

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

COPY core/go.mod core/go.sum ./
RUN go mod download

COPY core/ ./
RUN CGO_ENABLED=0 go build -o /core .

FROM ${DOCKER_MIRROR}alpine:3.19
ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /core /core
# SQL migrations (read by MIGRATIONS_DIR, default /migrations in docker-compose)
COPY --from=build /app/migrations /migrations
# Model catalog YAML (seeded at startup from ./config/model_catalog.yaml)
COPY --from=build /app/config /app/config
EXPOSE 8000
CMD ["/core"]
