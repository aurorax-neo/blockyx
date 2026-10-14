# blockyx: blocky DNS + web UI as a single binary.
# The Next.js UI is statically exported and embedded into the Go binary;
# the binary serves DNS, the HTTP API and the UI by itself.
# Usage: docker build -t blockyx . && docker run -p 53:53/udp -p 53:53/tcp \
#   -p 4000:4000 -v ./config.yml:/etc/blockyx/config.yml blockyx

# ---------- web UI builder (static export) ----------
FROM node:24-alpine AS frontend-builder
WORKDIR /build/frontend
ARG BUN_VERSION=1.4.2
RUN apk add --no-cache g++ make python3 \
  && npm install --global bun@${BUN_VERSION}
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build

# ---------- Go builder ----------
FROM golang:1.26.2-alpine AS go-builder
WORKDIR /build/backend
ARG VERSION=dev
ARG TARGETARCH
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Embed the statically exported UI into the binary.
COPY --from=frontend-builder /build/frontend/out ./web/ui-out
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X github.com/0xERR0R/blocky/util.Version=${VERSION} -X github.com/0xERR0R/blocky/util.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/0xERR0R/blocky/util.Architecture=${TARGETARCH}" \
  -o /blockyx .

# ---------- runner (single binary) ----------
FROM alpine:3.24
WORKDIR /app

LABEL org.opencontainers.image.source="https://github.com/blockyx/blockyx"
LABEL org.opencontainers.image.description="blockyx: blocky DNS server with embedded web UI, API key auth and query logs"

RUN apk add --no-cache ca-certificates \
  && addgroup --system --gid 1001 blockyx \
  && adduser --system --uid 1001 --ingroup blockyx blockyx \
  && mkdir -p /etc/blockyx /logs /data \
  && chown blockyx:blockyx /etc/blockyx /logs /data

COPY --from=go-builder /blockyx ./blockyx
COPY config.yml.example ./config.yml.example
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x entrypoint.sh && chown blockyx:blockyx entrypoint.sh

USER blockyx

EXPOSE 53/udp 53/tcp 4000

# The web UI root (/) is unauthenticated; the API needs the key.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:4000/ || exit 1

ENV BLOCKYX_CONFIG=/etc/blockyx/config.yml

ENTRYPOINT ["./entrypoint.sh"]
