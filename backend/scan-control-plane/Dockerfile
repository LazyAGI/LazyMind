# syntax=docker/dockerfile:1

ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}golang:1.25-bookworm AS build

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

WORKDIR /src
COPY scan-control-plane/go.mod scan-control-plane/go.sum ./scan-control-plane/
COPY core/go.mod core/go.sum ./core/
RUN cd scan-control-plane && go mod download

COPY scan-control-plane/cmd ./scan-control-plane/cmd
COPY scan-control-plane/internal ./scan-control-plane/internal
COPY scan-control-plane/migrations ./scan-control-plane/migrations
COPY scan-control-plane/scripts ./scan-control-plane/scripts
COPY core/state ./core/state

WORKDIR /src/scan-control-plane
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/scan-control-plane ./cmd/scan-control-plane

FROM ${DOCKER_MIRROR}alpine:3.21

ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata

RUN addgroup -S nonroot && adduser -S nonroot -G nonroot

WORKDIR /app
COPY --from=build /out/scan-control-plane /app/scan-control-plane
COPY --from=build /src/scan-control-plane/migrations /app/migrations
COPY --from=build /src/scan-control-plane/scripts /app/scripts

ENV LAZYMIND_SCAN_CONTROL_PLANE_ADDRESS=0.0.0.0
ENV LAZYMIND_SCAN_CONTROL_PLANE_PORT=18080
EXPOSE 18080

USER nonroot:nonroot
ENTRYPOINT ["/app/scan-control-plane"]
