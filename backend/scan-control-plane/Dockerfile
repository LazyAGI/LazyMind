ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}golang:1.25-bookworm AS build

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ARG TARGETARCH
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY scripts ./scripts

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -o /out/scan-control-plane ./cmd/scan-control-plane

FROM ${DOCKER_MIRROR}alpine:3.21

ARG ALPINE_MIRROR=https://mirrors.aliyun.com/alpine
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata

RUN addgroup -S nonroot && adduser -S nonroot -G nonroot

WORKDIR /app
COPY --from=build /out/scan-control-plane /app/scan-control-plane
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/scripts /app/scripts
RUN chmod -R a+rX /app/migrations /app/scripts

ENV LAZYMIND_SCAN_CONTROL_PLANE_ADDRESS=0.0.0.0
ENV LAZYMIND_SCAN_CONTROL_PLANE_PORT=18080
EXPOSE 18080

USER nonroot:nonroot
ENTRYPOINT ["/app/scan-control-plane"]
