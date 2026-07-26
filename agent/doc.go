// Package agent provides J's experimental model and tool runtime.
//
// The package intentionally exposes only the independent variation axes needed
// to embed the runtime: models, tools, messages, and per-run events. Queueing,
// transports, provider policy, retries, and model selection remain outside this
// package.
//
// The API is experimental until it has been validated by independent model and
// tool adapters.
package agent
