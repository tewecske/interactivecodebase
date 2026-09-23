# icb in a container: analyzes the Go module mounted at /src and serves the
# web UI, JSON API and MCP on port 8080. Analysis runs `go list`, so the
# runtime image keeps the Go toolchain; mount a volume at /go/pkg/mod to
# cache the analyzed module's dependencies.
FROM node:22-bookworm-slim AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -o /icb ./cmd/icb

FROM golang:1.26
COPY --from=build /icb /usr/local/bin/icb
RUN useradd --create-home --uid 10001 icb && mkdir -p /go/pkg/mod && chown -R icb /go
USER icb
EXPOSE 8080
# Authentication is always on: set ICB_TOKEN, or read the generated token
# from the container log.
ENTRYPOINT ["icb", "serve", "-addr", "0.0.0.0:8080"]
CMD ["/src"]
