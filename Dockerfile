FROM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.work ./
COPY J-agent/go.mod J-agent/go.mod
COPY J-mcp/go.mod J-mcp/go.sum J-mcp/
COPY J-mem/go.mod J-mem/go.sum J-mem/
COPY J-skills/go.mod J-skills/go.sum J-skills/
COPY J-subagents/go.mod J-subagents/
COPY J-tui/go.mod J-tui/go.sum J-tui/

RUN cd J-agent && go mod download
RUN cd J-mcp && go mod download
RUN cd J-mem && go mod download
RUN cd J-skills && go mod download
RUN cd J-subagents && go mod download
RUN cd J-tui && go mod download

COPY J-agent J-agent
COPY J-mcp J-mcp
COPY J-mem J-mem
COPY J-skills J-skills
COPY J-subagents J-subagents
COPY J-tui J-tui

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/j-agent ./J-agent/cmd/j-agent
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/j-tui ./J-tui/cmd/j-tui

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --uid 10001 --create-home --shell /bin/bash j \
	&& mkdir -p /workspace \
	&& chown j:j /workspace

COPY --from=build /out/j-agent /usr/local/bin/j-agent
COPY --from=build /out/j-tui /usr/local/bin/j-tui

ENV HOME=/home/j
WORKDIR /workspace
USER j

ENTRYPOINT ["j-agent"]
