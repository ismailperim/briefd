# syntax=docker/dockerfile:1
# Build context is the repository root:  docker build -f deploy/Dockerfile .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/briefd ./cmd/briefd
# The data volume must be writable by the non-root runtime user.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# briefd is pure Go: no libc, no git binary, no ONNX runtime needed.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/briefd /briefd
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV BRIEFD_LISTEN=:7788 \
    BRIEFD_DB=/data/briefd.db \
    BRIEFD_GIT_DIR=/data/knowledge-repo \
    BRIEFD_EMBEDDINGS_MODEL_DIR=/data/models/all-MiniLM-L6-v2 \
    HOME=/data
VOLUME ["/data"]
EXPOSE 7788
ENTRYPOINT ["/briefd"]
CMD ["serve"]
