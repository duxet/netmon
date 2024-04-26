build-server:
    go build

build-client:
    cd client && npm run build

build: build-client build-server
