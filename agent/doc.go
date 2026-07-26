// Package agent provides J's experimental model and tool runtime.
//
// The package intentionally exposes only the independent variation axes needed
// to embed the runtime: ordered model content, streaming deltas, completion
// observations, tools, and per-run events. Queueing, transports, provider
// policy, retries, and model selection remain outside this package.
//
// The API remains experimental while real external consumers validate the seam.
package agent
