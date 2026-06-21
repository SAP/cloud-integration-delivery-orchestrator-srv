# Multi-stage Dockerfile for cpi-delivery
#
# Prerequisites: web/dist/ must be populated with frontend build output before building.
#   cd ../mmt-devops-ui-cpi-delivery && npm run build
#   cp -r dist ../mmt-devops-cpi-delivery/web/dist
#
# Or use the Makefile in cpi-delivery-product which orchestrates this.

# === Build stage: compile Go binary with embedded frontend ===
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /cpi-delivery .

# === Runtime stage ===
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /cpi-delivery /usr/local/bin/cpi-delivery

EXPOSE 8080
ENTRYPOINT ["cpi-delivery"]
