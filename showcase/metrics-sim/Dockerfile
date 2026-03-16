FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go .
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 go build -o /metrics-sim .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /metrics-sim /metrics-sim
EXPOSE 9090
ENTRYPOINT ["/metrics-sim"]
