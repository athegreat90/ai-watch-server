# syntax=docker/dockerfile:1
# AI Watch Server: a static Go binary on a distroless base. No shell, package manager or Python.
# Both base images are pinned by digest; security/refresh_base.py keeps them current.

# The compiler always runs natively and cross-compiles, so arm64 images need no emulation to build.
FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN export CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    && go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" \
       -o /out/ai-watch-server ./cmd/ai-watch-server \
    && go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" \
       -o /out/ai-watch-check-api ./cmd/ai-watch-check-api

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
WORKDIR /app
COPY --from=build /out/ai-watch-server /out/ai-watch-check-api /app/

# UID 1000 matches the owner of the 0600 token file on a typical Linux/Raspberry Pi host, so the
# file stays private and still readable. Override with --user (or PUID/PGID in compose) if needed.
USER 1000:1000
EXPOSE 8765

HEALTHCHECK --interval=30s --timeout=4s --start-period=10s --retries=3 \
    CMD ["/app/ai-watch-server", "healthcheck"]

# The image also carries the read-only API checker, so a deployment can verify itself with
# docker compose exec ai-watch-server /app/ai-watch-check-api --token-file /app/config.local.json
# The config is mounted at /app/config.local.json (the working directory), the default path.
ENTRYPOINT ["/app/ai-watch-server"]
CMD ["serve"]
