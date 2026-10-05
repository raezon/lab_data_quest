# Image du jeu CloudQuest (serveur web). Les émulateurs tournent à côté :
# docker-compose.yml en local, k8s/ sur une plateforme partagée.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /cloudquest ./cmd/cloudquest

FROM alpine:3.20
RUN adduser -D -u 1000 cloudquest && mkdir /data && chown cloudquest /data
COPY --from=build /cloudquest /usr/local/bin/cloudquest
COPY docs/cahier-de-projet.pdf /srv/docs/cahier-de-projet.pdf
USER 1000
ENV CQ_ADDR=0.0.0.0:8090 CQ_DATA=/data/progress.json CQ_DOCS=/srv/docs
EXPOSE 8090
ENTRYPOINT ["cloudquest"]
