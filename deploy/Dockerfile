# wly, the server-side daemon. The Minecraft container is itzg's published
# image and is never built here.
#
# Build from the repository root:
#   docker build -f deploy/Dockerfile -t wly:local .

# --platform=$BUILDPLATFORM pins the BUILDER to the runner's own architecture,
# and Go cross-compiles to the target from there. Without it, `buildx --platform
# linux/arm64` on an amd64 runner emulates an entire arm64 CPU through QEMU for
# every instruction below, the test suite included. Measured on v0.3.0: the
# image job took 10m19s, while the identical vet and tests took 1m19s natively
# in the gate job.
#
# Go has cross-compiled since forever and CGO is off here, so the emulation was
# buying nothing at all.
FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS build
WORKDIR /src

# Supplied by buildx. TARGETARCH is what we build FOR; the toolchain above runs
# natively on whatever the runner happens to be.
ARG TARGETARCH
ARG TARGETOS=linux

# Dependencies first, so a source-only change does not refetch the module cache.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=""

# The tests run inside the build: an image cannot be produced from a tree whose
# suite is red. -trimpath and an empty -buildid keep the binary reproducible,
# so the image and the release asset for a tag are byte-identical.
# These run NATIVELY on the runner now rather than under emulation, which is
# where the ten minutes went. They still gate the image: a tree whose suite is
# red cannot produce one.
#
# The trade, stated plainly: the suite runs on the runner's architecture rather
# than the target's. Nothing here is architecture-specific, ci.yml already runs
# the same suite on amd64, and -race needs cgo so it never ran in this build
# anyway. If something ever does depend on arm64 it wants a real arm64 runner,
# not a QEMU one: a test that takes ten minutes to emulate is a test nobody
# keeps.
RUN go vet ./... && go test -count=1 ./...
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags="-s -w -buildid= -X weloveyou-mc/internal/buildinfo.version=${VERSION} -X weloveyou-mc/internal/buildinfo.commit=${COMMIT}" \
      -o /out/wly ./cmd/wly

# distroless static: no shell, no package manager, nothing for a compromised
# process to pivot into. wly needs only the CA bundle, for R2 and Discord.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/wly /wly
USER nonroot:nonroot
ENTRYPOINT ["/wly"]
CMD ["serve"]
