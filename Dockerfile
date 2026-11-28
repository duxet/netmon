# syntax=docker/dockerfile:1

# netmon in a container: the web UI is built once, netmon is cross compiled
# for the image's platform, as the release workflow builds it, and the image
# has nothing else but the time zones. Build it for one or more platforms:
#
#   docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=v1.2.3 .

FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS client
WORKDIR /src/client
COPY client/package.json client/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY client/ ./
RUN npm run build

# Debian 13 rather than 12: 12's arm64 libc.a overflows its GOT when linked
# statically with DuckDB ("relocation truncated to fit: R_AARCH64_LD64_GOTPAGE_LO15").
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS go
ARG TARGETARCH
# A release's tag, such as v1.2.3, which netmon -version and the web UI show.
ARG VERSION=dev

# DuckDB's static libraries only need a C and C++ compiler for the platform,
# when it isn't the builder's: a cross compiler is installed, and named in
# /cross.env for the build.
RUN case "$TARGETARCH" in \
      amd64) triple=x86_64-linux-gnu ;; \
      arm64) triple=aarch64-linux-gnu ;; \
      *) echo "netmon can't be built for $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    if [ "$TARGETARCH" = "$(go env GOARCH)" ]; then \
      touch /cross.env; \
    else \
      package="$(echo "$triple" | tr _ -)" && \
      apt-get update && \
      apt-get install -y --no-install-recommends "gcc-$package" "g++-$package" && \
      rm -rf /var/lib/apt/lists/* && \
      echo "export CC=$triple-gcc CXX=$triple-g++" > /cross.env; \
    fi

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
COPY --from=client /src/client/dist client/dist

# Static, with Go's own resolver and user lookups, like the release binaries.
RUN . /cross.env && \
    CGO_ENABLED=1 GOOS=linux GOARCH="$TARGETARCH" go build -trimpath -tags netgo,osusergo \
      -ldflags "-s -w -extldflags=-static -X github.com/duxet/netmon/version.Version=${VERSION#v}" \
      -o /out/netmon . && \
    mkdir /out/data

FROM alpine:3.21
# Alpine has the CA certificates already; the time zones are for quiet hours
# in the TZ the container is given.
COPY --from=go /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=go /out/netmon /usr/bin/netmon
COPY --from=go /out/data /data

# flows.db, netmon.yaml and the GeoLite2 databases go here.
WORKDIR /data
VOLUME /data
EXPOSE 2137
HEALTHCHECK --start-period=30s CMD ["/usr/bin/netmon", "-healthcheck"]
ENTRYPOINT ["/usr/bin/netmon"]
