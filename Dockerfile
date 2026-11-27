FROM golang:1.27 AS go

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN go build -ldflags="-extldflags=-static"

FROM alpine:latest

COPY --from=go /src/netmon /usr/bin

HEALTHCHECK CMD ["/usr/bin/netmon", "-healthcheck"]

CMD ["/usr/bin/netmon"]
