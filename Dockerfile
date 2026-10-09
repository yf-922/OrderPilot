FROM m.daocloud.io/docker.io/library/golang:1.26.9-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/product ./backend

FROM m.daocloud.io/docker.io/library/alpine:3.22
WORKDIR /app
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/product /app/product
COPY backend/web /app/backend/web
USER app
EXPOSE 8080
CMD ["/app/product"]
