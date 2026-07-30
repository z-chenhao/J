# J-Space Workbench

J-Space Workbench is J's independent top-level interpretability module and
J-tui Observer implementation. It aligns
J-agent lifecycle events with a Jacobian-lens replay of the exact model-turn
token sequence, then renders the result as an interactive layer × token view.

It does not add Python, MLX, model weights, fitted lenses, visualization
concepts, or deployment policy to the J-agent runtime.

## What is measured

Anthropic's Jacobian lens transports a residual-stream vector at layer `l`
into the final-layer basis and decodes it with the model's own unembedding:

```text
lens_l(h) = unembed(J_l @ h)
J_l = E[d h_final / d h_l]
```

The resulting ranked vocabulary tokens indicate concepts that the activation
is disposed to make the model verbalize later. They are not generated text,
private chain-of-thought, a complete thought, or ground truth.

The workbench has two explicitly separate clocks:

1. J-agent events are recorded live while the configured model/tool run occurs.
2. After each model turn completes, the exact request plus returned assistant
   message is replayed through an instrumented local checkpoint. The replay
   reads residual activations and applies the matching fitted J-lens.

For a causal decoder, replaying the same token prefix recovers the same
autoregressive computation in principle. Quantized kernels, cache layout, MTP,
and the separate process can still introduce numerical differences, so the UI
labels this as `posthoc_replay`; it never presents it as a provider-native live
activation stream.

## Supported local profile

The first validated profile targets:

- runtime model: `Qwen3.6-35B-A3B-oQ4e-mtp`
- checkpoint family: `Qwen/Qwen3.6-35B-A3B`
- local model path:
  `~/.omlx/models/Jundot/Qwen3.6-35B-A3B-oQ4e-mtp`
- fitted lens:
  `stanleytheli/qwen3.6-35B-A3B-jlens`, `lens.pt`
- 40 decoder layers, residual width 2048, vocabulary size 248,320

The fitted lens was produced for the unquantized base checkpoint. Applying it
to an oQ4e/oQ5e mixed-quantization checkpoint is useful experimental evidence,
not proof that quantization leaves J-space geometry unchanged. The artifact
records both identifiers so comparisons cannot silently mix them.

The pinned lens revision currently validates as 39 source matrices (layers
0–38), each 2048 × 2048 in float16, fitted from 1,000 prompts. The final layer
is decoded directly, matching the reference implementation. The fetch helper
prints the exact revision, file size, and SHA-256.

## Quick start

Build the dependency-free Go programs:

```bash
make build
```

Download the fitted lens into local research state:

```bash
make fetch-lens
```

Start the local viewer:

```bash
./bin/jspace-server
```

Open <http://127.0.0.1:8090/jspace/>.

Record one J-agent run and then perform the J-lens replay:

```bash
./bin/jspace-record \
  --label "two-hop reasoning" \
  --prompt "How many legs does the animal that spins webs have?"
```

`jspace-record` uses the public J-agent `Model`, `Tool`, and `EventHandler`
seams. It defaults to the repository's local oMLX profile and reads the oMLX
API key into the child process environment without placing the secret in
arguments or artifacts.

The MLX probe runs with oMLX's Python environment by default. Override paths
with flags or environment variables when the host differs:

```text
JSPACE_MODEL_PATH
JSPACE_LENS_PATH
JSPACE_STATE_DIR
JSPACE_PROBE_PYTHON
JSPACE_OMLX_SETTINGS
```

Raw prompts, reasoning, tool results, and model responses are held only long
enough to construct the replay input. The public artifact stores event kinds,
timings, usage, token labels, and J-lens readouts. Pass
`--retain-transcript` only for an explicitly private experiment.

## J-tui Observer

Install the released binaries:

```bash
curl --proto '=https' --tlsv1.2 --fail --location \
  https://raw.githubusercontent.com/z-chenhao/J/main/J-space/install.sh | sh
```

The 0.2 installer unregisters the retired `dev.usej.jspace` J Package entry if
it exists, while retaining the package cache. Observation is now configured
directly by the J-tui product that owns the protocol.

For repository development, `go install ./cmd/jspace-observer` provides the
same executable.

```json
{
  "extensions": {
    "observers": {
      "jspace": {
        "command": "jspace-observer",
        "env": ["JSPACE_CAPTURE_URL", "JSPACE_CAPTURE_TOKEN"],
        "permissions": ["agent.events", "model.frames"]
      }
    }
  }
}
```

Export the J-Space-owned delivery configuration:

```bash
export JSPACE_CAPTURE_URL="https://usej-model.tailb0426d.ts.net/jspace/api/captures"
export JSPACE_CAPTURE_TOKEN="..."
```

Installing the binary does not activate it. The explicit J-tui entry grants
only its listed `agent.events` and `model.frames` projections. J-tui invokes
the Observer with one bounded `j.observer.run.v0.1` value after a run.
The Observer then submits a J-Space-owned `jspace.capture.v0.1` request to:

```text
POST /jspace/api/captures
```

The endpoint uses a separate mode-0600 token at
`~/.j/jspace/capture-token`. It does not accept the viewer token. The gateway
bounds and validates the request, persists it in the private
`~/.j/jspace/inbox/` queue, and returns `202` before the single local replay
worker loads MLX. The viewer therefore observes `probing` before `completed`
or an explicit `partial` result.

Remote raw frames are deleted from the inbox after either result. They are
never added to the public artifact. A process restart resumes private inbox
files, and the J-Space Observer retains retryable failed deliveries in its own
mode-0600 outbox. Observer failure never changes the authoritative J-agent
result.

## Public read-only viewer

The repository includes `jspace-gateway`, a narrow reverse proxy intended for
the existing Tailscale Funnel deployment:

```text
https://usej-model.tailb0426d.ts.net/jspace/
```

The page shell and explicitly illustrative `/jspace/api/demo` artifact are
public. Every endpoint containing real observations requires a separate viewer
token. The gateway only allows `GET` and `HEAD` for J-space paths, so a public
client cannot start Agent runs, import artifacts, change models, or invoke
tools. The local server independently verifies the same token.

On first start the gateway creates a mode-0600 token file at
`~/.j/jspace/access-token` if one does not exist. The token is never logged or
embedded in the page. Paste it into the page's unlock form; the browser keeps
it in `sessionStorage`, not persistent storage or the URL.

Deployment files and lifecycle commands are documented in
[`deploy/README.md`](deploy/README.md).

## Artifact contract

Artifacts use the experimental `jspace.trace.v0.1` schema. Each file records:

- J commit, model checkpoint, quantization, lens repository, and lens digest;
- measurement kind and replay-fidelity limitations;
- safe J-agent lifecycle events and normalized usage;
- selected token positions;
- per-position, per-layer top J-lens tokens and ranks;
- failures and partial results without aggregating them away.

The schema is owned by this research module and is not a J-agent public
contract. A second independent viewer or probe must validate it before any
stability promise.

## Research use

Pre-register task-relevant concepts before inspecting a result. Compare one
harness change at a time while holding the model, tokenizer, lens, generation
parameters, tools, task corpus, hardware, and seed constant.

J-lens evidence may explain a behavioral change, but it does not replace task
success, invalid-action, latency, token, and safety evaluation. No harness
default should change from J-space evidence alone.

## Primary sources

- <https://www.anthropic.com/research/global-workspace>
- <https://transformer-circuits.pub/2026/workspace/>
- <https://github.com/anthropics/jacobian-lens>
- <https://huggingface.co/stanleytheli/qwen3.6-35B-A3B-jlens>
