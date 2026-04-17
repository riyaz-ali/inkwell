FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o inkwell .

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/inkwell .
COPY web/ ./web/
EXPOSE 8080
CMD ["./inkwell"]
