# Build the API
FROM golang:1.21.6-alpine AS builder

ENV GO111MODULE=on

WORKDIR /app/build

COPY go.mod .
COPY go.sum .

RUN go mod download
COPY . .

# Build the Go binary
RUN go build -o model-api .

# Final stage
FROM alpine:latest

# Install nginx and supervisor
RUN apk add --no-cache nginx supervisor

# Copy the Go binary from builder
COPY --from=builder /app/build/model-api /app/build/model-api

# Create directory for static files
# RUN mkdir -p /usr/share/nginx/html/app/viewer

# Copy static files
# COPY static/ /usr/share/nginx/html/app/viewer/

# Copy the nginx configuration
COPY nginx.conf /etc/nginx/nginx.conf

# Copy supervisor configuration
COPY supervisord.conf /etc/supervisord.conf

# Only expose port 80
EXPOSE 80

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]

