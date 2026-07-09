# Step 1: Build the Go executable binary container
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./

# Add environment override to force the compiler to bypass local toolchain blocks
ENV GOTOOLCHAIN=auto
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o binitradar .

# Step 2: Assemble production image
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/binitradar .
COPY --from=builder /app/binitbot-key.json .
COPY --from=builder /app/web ./web

EXPOSE 8080
CMD ["./binitradar"]