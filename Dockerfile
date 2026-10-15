# blockyx: blocky DNS + web UI as a single binary.
# Usage: docker build -t blockyx . && docker run -p 53:53/udp -p 53:53/tcp \
#   -p 4000:4000 -v ./config.yml:/etc/blockyx/config.yml blockyx

# ---------- web UI builder (static export) ----------
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder
WORKDIR /build/frontend
ARG BUN_VERSION=1.4.2
RUN apk add --no-cache g++ make python3 \
  && npm install --global bun@${BUN_VERSION}
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build

# ---------- Go builder ----------
FROM --platform=$BUILDPLATFORM golang:1.26.2-alpine AS go-builder
WORKDIR /build/backend
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN apk add --no-cache git ca-certificates
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Embed the statically exported UI into the binary.
COPY --from=frontend-builder /build/frontend/out ./web/ui-out
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} go build -trimpath \
  -ldflags="-s -w -X github.com/0xERR0R/blocky/util.Version=${VERSION} -X github.com/0xERR0R/blocky/util.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/0xERR0R/blocky/util.Architecture=${TARGETARCH}${TARGETVARIANT}" \
  -o /blockyx .
# ---------- runner ----------
FROM alpine:3.24
WORKDIR /app

LABEL org.opencontainers.image.source="https://github.com/aurorax-neo/blockyx"
LABEL org.opencontainers.image.description="blockyx: blocky DNS server with embedded web UI, API key auth and query logs"

RUN apk add --no-cache ca-certificates su-exec libcap \
  && addgroup --system --gid 1001 blockyx \
  && adduser --system --uid 1001 --ingroup blockyx blockyx \
  && mkdir -p /etc/blockyx /logs /data

COPY --from=go-builder /blockyx ./blockyx
COPY config.yml.example ./config.yml.example
COPY entrypoint.sh ./entrypoint.sh
# File capability is permitted, not effective. The process raises it when the
# runtime still allows CAP_NET_BIND_SERVICE. +ep can be rejected by a runtime
# that drops every capability before exec.
RUN setcap 'cap_net_bind_service=+p' ./blockyx \
  && chmod +x entrypoint.sh \
  && chown blockyx:blockyx ./blockyx ./entrypoint.sh

EXPOSE 53/udp 53/tcp 4000

ENV BLOCKYX_CONFIG=/etc/blockyx/config.yml

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ./blockyx --config "$BLOCKYX_CONFIG" healthcheck

ENTRYPOINT ["./entrypoint.sh"]
