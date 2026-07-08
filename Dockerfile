# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26.5-alpine AS build

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build so the binary runs in a scratch/distroless image.
# Migrations and web assets are embedded via go:embed, so the binary is self-contained.
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w" \
      -o /out/meshtender ./cmd/meshtender


# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/meshtender /usr/local/bin/meshtender

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/meshtender"]
