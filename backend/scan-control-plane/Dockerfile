# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY scripts ./scripts

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/scan-control-plane ./cmd/scan-control-plane

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/scan-control-plane /app/scan-control-plane
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/scripts /app/scripts

ENV SCAN_CONTROL_PLANE_ADDRESS=0.0.0.0
ENV SCAN_CONTROL_PLANE_PORT=18080
EXPOSE 18080

USER nonroot:nonroot
ENTRYPOINT ["/app/scan-control-plane"]
