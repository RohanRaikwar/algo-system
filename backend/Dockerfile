# Backend image: builds the five services and runs them in one container.
# They share one SQLite data dir, so they must live together (a Railway
# volume can only attach to one service). Redis runs separately.

FROM golang:1.21-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO is required by go-sqlite3.
RUN set -e; for svc in mdengine indengine stratengine analyst api_gateway; do \
      CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/$svc ./cmd/$svc/; \
    done

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
ENV TZ=Asia/Kolkata
WORKDIR /app
COPY --from=build /out/ /app/bin/
COPY config/ /app/config/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh && mkdir -p /app/data /app/logs /app/.cache
EXPOSE 9090
ENTRYPOINT ["/app/docker-entrypoint.sh"]
