FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY worker/ ./worker/

RUN CGO_ENABLED=0 GOOS=linux go build -o worker-node ./worker/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/worker-node .

EXPOSE 8081

CMD ["./worker-node"]