# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.2 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build the controller-manager and Fabriclet independently so each final image
# contains only the binary it needs.
FROM builder AS fabric-controller-manager-builder
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o fabric-controller-manager ./cmd/fabric-controller-manager

FROM builder AS fabriclet-sonic-builder
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o fabriclet-sonic ./cmd/fabriclet-sonic

FROM gcr.io/distroless/static:nonroot AS fabric-controller-manager
WORKDIR /
COPY --from=fabric-controller-manager-builder /workspace/fabric-controller-manager /fabric-controller-manager
USER 65532:65532

ENTRYPOINT ["/fabric-controller-manager"]

FROM debian:bookworm-slim AS fabriclet-sonic
RUN apt-get update \
    && apt-get install -y --no-install-recommends iproute2 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=fabriclet-sonic-builder /workspace/fabriclet-sonic /fabriclet-sonic

ENTRYPOINT ["/fabriclet-sonic"]
