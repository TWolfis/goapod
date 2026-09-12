FROM golang:1.25

WORKDIR /app

# root module (github.com/TWolfis/goapod), needed to satisfy the
# `replace github.com/TWolfis/goapod => ../..` in cmd/afetch/go.mod
COPY go.mod go.sum goapod.go ./

# afetch module, kept two levels down so ../.. still resolves to /app
COPY cmd/afetch/go.mod cmd/afetch/go.sum ./cmd/afetch/
WORKDIR /app/cmd/afetch
RUN go mod download

COPY cmd/afetch/*.go ./

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /afetch

CMD ["/afetch"]
