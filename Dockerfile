# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder
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
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /out/fabric-controller-manager ./cmd/fabric-controller-manager

FROM builder AS fabriclet-sonic-builder
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /out/fabriclet-sonic ./cmd/fabriclet-sonic

FROM gcr.io/distroless/static:nonroot AS fabric-controller-manager
WORKDIR /
COPY --from=fabric-controller-manager-builder /out/fabric-controller-manager /fabric-controller-manager
USER 65532:65532

ENTRYPOINT ["/fabric-controller-manager"]

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot AS fabriclet-sonic
WORKDIR /
COPY --from=fabriclet-sonic-builder /out/fabriclet-sonic /fabriclet-sonic
USER 65532:65532

ENTRYPOINT ["/fabriclet-sonic"]
