# builder 阶段始终运行在构建机原生平台（amd64），用 Go 交叉编译目标平台二进制
FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o kiro-go .

FROM alpine:latest
ARG TARGETARCH
ARG KIRO_CLI_VERSION=latest
RUN apk --no-cache add ca-certificates curl unzip && \
    case "${TARGETARCH}" in \
      amd64) KIRO_CLI_ARCH="x86_64" ;; \
      arm64) KIRO_CLI_ARCH="aarch64" ;; \
      *) echo "unsupported Kiro CLI architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    KIRO_CLI_ZIP="kirocli-${KIRO_CLI_ARCH}-linux-musl.zip" && \
    KIRO_CLI_URL="https://desktop-release.q.us-east-1.amazonaws.com/${KIRO_CLI_VERSION}/${KIRO_CLI_ZIP}" && \
    curl -fsSL "${KIRO_CLI_URL}" -o "/tmp/${KIRO_CLI_ZIP}" && \
    unzip -q "/tmp/${KIRO_CLI_ZIP}" -d /opt && \
    ln -sf /opt/kirocli/bin/kiro-cli /usr/local/bin/kiro-cli && \
    ln -sf /opt/kirocli/bin/kiro-cli /usr/local/bin/kiro && \
    rm -f "/tmp/${KIRO_CLI_ZIP}"

WORKDIR /app
COPY --from=builder /app/kiro-go .
COPY --from=builder /app/web ./web

EXPOSE 8080
VOLUME /app/data

ENV KIRO_CLI_HOME=/app/data/kiro-cli

CMD ["./kiro-go"]
