# wly — the server-side daemon. The Minecraft container is itzg's published
# image and is never built here.
#
# Build from the repository root:
#   docker build -f deploy/Dockerfile -t wly:local .

FROM golang:1.26.6-alpine AS build
WORKDIR /src

# Dependencies first, so a source-only change does not refetch the module cache.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=""

# The tests run inside the build: an image cannot be produced from a tree whose
# suite is red. -trimpath and an empty -buildid keep the binary reproducible,
# so the image and the release asset for a tag are byte-identical.
RUN go vet ./... && go test -count=1 ./...
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -buildid= -X weloveyou-mc/internal/buildinfo.version=${VERSION} -X weloveyou-mc/internal/buildinfo.commit=${COMMIT}" \
      -o /out/wly ./cmd/wly

# distroless static: no shell, no package manager, nothing for a compromised
# process to pivot into. wly needs only the CA bundle, for R2 and Discord.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/wly /wly
USER nonroot:nonroot
ENTRYPOINT ["/wly"]
CMD ["serve"]
