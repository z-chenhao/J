// Package agent provides J-agent's experimental model and tool runtime.
//
// The package intentionally exposes only the independent variation axes needed
// to embed the runtime: ordered model content, streaming deltas, completion
// observations, tools, and per-run events. Queueing, transports, provider
// policy, retries, and model selection remain outside this package.
//
// One Agent owns one serialized conversation. A run commits its user message
// and every completed model/tool message. Invalid or over-limit model output is
// not committed. History returns a defensive snapshot; WithHistory validates
// and restores such a snapshot only during construction. Persistence and
// session identity remain outside the package. Event handlers are synchronous
// observations and must not reenter Run or Reset on the same Agent.
//
// The API remains experimental while real external consumers validate the seam.
package agent
