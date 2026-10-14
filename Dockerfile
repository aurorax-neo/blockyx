# blockyx: blocky DNS + web UI as a single binary.
# Minimal scratch image: static Go binary + ca-certificates only.
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
RUN apk add --no-cache git ca-certificates
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Embed the statically exported UI into the binary.
COPY --from=frontend-builder /build/frontend/out ./web/ui-out
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X github.com/0xERR0R/blocky/util.Version=${VERSION} -X github.com/0xERR0R/blocky/util.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/0xERR0R/blocky/util.Architecture=${TARGETARCH}" \
  -o /blockyx . \
  && /blockyx --version

# ---------- runner (scratch, minimal) ----------
FROM scratch

LABEL org.opencontainers.image.source="https://github.com/aurorax-neo/blockyx"
LABEL org.opencontainers.image.description="blockyx: blocky DNS server with embedded web UI, API key auth and query logs"

# ca-certificates for DoH/DoT upstreams
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-builder /blockyx /blockyx
COPY config.yml.example /config.yml.example

# Run as non-root (nobody:65534)
USER 65534:65534

EXPOSE 53/udp 53/tcp 4000

ENV BLOCKYX_CONFIG=/etc/blockyx/config.yml

ENTRYPOINT ["/blockyx"]
CMD ["--config", "/etc/blockyx/config.yml"]
