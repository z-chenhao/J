FROM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.work ./
COPY J-agent/go.mod J-agent/go.mod
COPY J-agent/research/jspace/go.mod J-agent/research/jspace/go.mod
COPY J-mcp/go.mod J-mcp/go.sum J-mcp/
COPY J-mem/go.mod J-mem/go.sum J-mem/
COPY J-packages/go.mod J-packages/go.sum J-packages/
COPY J-skills/go.mod J-skills/go.sum J-skills/
COPY J-subagents/go.mod J-subagents/go.sum J-subagents/
COPY J-tui/go.mod J-tui/go.sum J-tui/

RUN cd J-agent && GOWORK=off go mod download
RUN cd J-mcp && GOWORK=off go mod download
RUN cd J-mem && GOWORK=off go mod download
RUN cd J-packages && GOWORK=off go mod download
RUN cd J-skills && GOWORK=off go mod download
RUN cd J-subagents && GOWORK=off go mod download
RUN cd J-tui && go mod download

COPY J-agent J-agent
COPY J-mcp J-mcp
COPY J-mem J-mem
COPY J-packages J-packages
COPY J-skills J-skills
COPY J-subagents J-subagents
COPY J-tui J-tui

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/j-agent ./J-agent/cmd/j-agent
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/j-tui ./J-tui/cmd/j-tui
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/j ./J-packages/cmd/j

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --uid 10001 --create-home --shell /bin/bash j \
	&& mkdir -p /workspace \
	&& chown j:j /workspace

COPY --from=build /out/j-agent /usr/local/bin/j-agent
COPY --from=build /out/j-tui /usr/local/bin/j-tui
COPY --from=build /out/j /usr/local/bin/j

ENV HOME=/home/j
WORKDIR /workspace
USER j

ENTRYPOINT ["j-agent"]
