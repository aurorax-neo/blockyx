# blockyx: blocky DNS + web UI in a single image.
# Usage: docker build -t blockyx . && docker run -p 53:53/udp -p 53:53/tcp \
#   -p 3000:3000 -p 4000:4000 -v ./config.yml:/etc/blockyx/config.yml blockyx

# ---------- web UI builder ----------
FROM node:24-alpine AS webui-builder
WORKDIR /build/webui
ARG BUN_VERSION=1.4.2
RUN apk add --no-cache g++ make python3 \
  && npm install --global bun@${BUN_VERSION}
COPY webui/package.json webui/bun.lock ./
RUN bun install --frozen-lockfile
COPY webui/ ./
RUN SKIP_ENV_VALIDATION=true bun run build

# ---------- Go builder ----------
FROM golang:1.26.2-alpine AS go-builder
WORKDIR /build
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /blockyx .

# ---------- runner ----------
FROM alpine:3.24
WORKDIR /app
ENV NODE_ENV=production

LABEL org.opencontainers.image.source="https://github.com/blockyx/blockyx"
LABEL org.opencontainers.image.description="blockyx: blocky DNS server with web UI, API key auth and fine-grained query logs"

RUN apk add --no-cache nodejs ca-certificates \
  && addgroup --system --gid 1001 blockyx \
  && adduser --system --uid 1001 --ingroup blockyx blockyx \
  && mkdir -p /etc/blockyx /logs /data \
  && chown blockyx:blockyx /etc/blockyx /logs /data

COPY --from=go-builder /blockyx ./blockyx
COPY --from=webui-builder /build/webui/.next/standalone ./
COPY --from=webui-builder --chown=blockyx:blockyx /build/webui/.next/static ./.next/static
COPY --from=webui-builder /build/webui/public ./public
COPY config.yml.example ./config.yml.example
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x entrypoint.sh && chown blockyx:blockyx entrypoint.sh

USER blockyx

EXPOSE 53/udp 53/tcp 3000 4000

ENV BLOCKYX_CONFIG=/etc/blockyx/config.yml

ENTRYPOINT ["./entrypoint.sh"]
