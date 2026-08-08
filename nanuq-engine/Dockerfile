# =============================================================================
# Dockerfile — nanuq-server (meta-buscador SearXNG en Go, nanuq-engine)
# -----------------------------------------------------------------------------
# Etapa 1 (build): compila el binario estático con CGO desactivado para que
# corra en imágenes ligeras (scratch/alpine) sin dependencias dinámicas.
# Etapa 2 (final): imagen mínima basada en alpine — incluye los CA certs del
# sistema para que el cliente HTTPS (network.New) pueda validar certificados
# de los motores de búsqueda upstream.
# =============================================================================

# --- Stage 1: build ----------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Copiamos primero go.mod/go.sum para aprovechar la caché de capas de Docker:
# mientras las dependencias no cambien, `go mod download` no se re-ejecuta.
COPY go.mod go.sum ./
RUN go mod download

# Copiamos el resto del código fuente y compilamos.
COPY . .

# CGO_ENABLED=0 => binario estático, portable a cualquier imagen Linux.
# GOOS=linux explícito para garantizar el target del contenedor.
RUN CGO_ENABLED=0 GOOS=linux go build -o /nanuq-server ./cmd/nanuq-server

# --- Stage 2: runtime --------------------------------------------------------
FROM alpine:latest

# Certificados CA para conexiones HTTPS salientes (motores de búsqueda).
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Binario compilado + configuración por defecto.
COPY --from=build /nanuq-server /app/nanuq-server
COPY settings.yml /app/settings.yml

# Puerto HTTP del servidor (ver settings.yml -> server.port).
EXPOSE 8888

# nanuq-server lee -config para ubicar el settings.yml.
# Nota: dentro del contenedor el server escucha en 127.0.0.1 (bind_address
# por defecto); Docker publica el puerto hacia el host, así que sigue siendo
# accesible desde fuera.
CMD ["/app/nanuq-server", "-config", "/app/settings.yml"]
