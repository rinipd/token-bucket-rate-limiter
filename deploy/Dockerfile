# ---- Build stage ------------------------------------------------------------
# The full golang image is used only for this stage — its size doesn't matter
# because the stage itself is discarded after the build; only the compiled
# /server binary is copied into the runtime stage below. Using it (rather than
# a slimmer builder variant) keeps the build environment simple and matches
# the toolchain version go.mod expects.
FROM golang:1.26 AS builder

WORKDIR /src

# Copy just the dependency manifests first so `go mod download` is cached as
# its own Docker layer and only re-runs when go.mod/go.sum actually change,
# not on every source-code edit (which would otherwise bust the cache and
# re-download all dependencies on every build).
COPY go.mod go.sum ./
RUN go mod download

# Now copy the rest of the source and build.
COPY . .

# CGO_ENABLED=0 produces a fully static binary with no dynamic dependency on
# glibc or any other host C library. Without it, cgo links against the
# builder's C library at build time, and the runtime image would need a
# matching (or compatible) one present — which a minimal base image like
# alpine typically doesn't have. A static binary runs unmodified on any
# minimal base, which is what makes stage 2 possible at all.
RUN CGO_ENABLED=0 go build -o /server .

# ---- Runtime stage -----------------------------------------------------------
# alpine is a small (~5MB) base with just enough userland (shell, certs) to be
# convenient to debug (`docker exec ... sh`), while remaining far smaller than
# shipping the full golang build image into production — which would carry
# the entire Go compiler and stdlib sources along for no reason, since only
# the already-compiled binary needs to actually run here.
FROM alpine:latest

COPY --from=builder /server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
