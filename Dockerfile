# Dockerfile for launching an API server for a local Bitcoin node
FROM golang:1.20-alpine

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY *.go ./

# Run tests
RUN go test -v

RUN go build -o /bitcoin-api

EXPOSE 8080

CMD ["/bitcoin-api"]
