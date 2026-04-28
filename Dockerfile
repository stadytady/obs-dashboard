# --- builder ---
FROM --platform=$TARGETOS/$TARGETARCH golang:1.26-alpine3.22 AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# -trimpath убирает $GOPATH из путей в бинаре (репродьюсибельность + меньше утечек)
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kube-ctl .

# --- runtime ---
# distroless/static-debian12 содержит только ca-certificates + tzdata, без shell
# и пакетного менеджера — минимальная атак-поверхность. nonroot вариант пропускает
# uid 65532 в HOME=/home/nonroot, USER=nonroot:nonroot выставлен в самом образе.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /kube-ctl /kube-ctl
COPY frontend/ /static

EXPOSE 8080

# Самопроверка: бинарь идёт в режиме -healthcheck, дёргает /api/clusters на
# собственном listen-адресе. Не требует shell — работает в distroless.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/kube-ctl", "-healthcheck", "-addr=:8080"]

ENTRYPOINT ["/kube-ctl"]
CMD ["-addr=:8080", "-static=/static"]
