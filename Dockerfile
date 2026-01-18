# ビルドステージ
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# 依存関係をコピーしてダウンロード
COPY go.mod go.sum ./
RUN go mod download

# ソースコードをコピー
COPY main.go ./

# 静的バイナリをビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o leader-election main.go

# ランタイムステージ
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# ビルドステージからバイナリをコピー
COPY --from=builder /workspace/leader-election /leader-election

USER 65532:65532

ENTRYPOINT ["/leader-election"]
