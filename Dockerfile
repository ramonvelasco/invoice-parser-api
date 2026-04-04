FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o server .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/landing ./landing

RUN mkdir -p /data

ENV DB_PATH=/data/invoiceparser.db
ENV PORT=8080
ENV CORS_ORIGINS=""

EXPOSE 8080

CMD ["./server"]
