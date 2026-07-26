# Architectural Constitution

> Build systems that outlive their implementations.

# Mission (J-agent)

J-agent is a **minimal, customizable agent runtime**.

- Keep the kernel small: command parsing, task lifecycle, queueing, and a versioned canonical event protocol.
- Make extension easy: expose a clear adapter boundary for model, tools, transport, and richer command semantics.
- Be protocol-agnostic by default: emit a compact canonical stream and let downstream layers translate when needed.

Non-goals for the core runtime:

- Full feature parity with pi.
- Built-in plugin/external command ecosystems.
- UI, session trees, advanced compaction/retry policies, model fleet management.

Core invariants are always explicit. Experimental contracts are labeled as
such and become stable only after real consumers validate them. Extra
capabilities belong outside the core unless they directly reduce integration
cost.

The future `J` repository may compose J-agent with independent projects such as
J-tui and J-mem. Those projects are first-party examples, not J-agent
dependencies and not justification for speculative runtime interfaces.

J-Space and Jacobian-lens work belongs under `research/` and is not a runtime
dependency. It may inform model harness defaults only after reproducible
J-lens observations and behavioral benchmarks validate the same concrete
improvement.

This document defines the fundamental philosophy behind every project.
Technology changes.
Models change.
Frameworks change.
Products come and go.

These principles should remain stable.

---

# Principle 1 — Openness over Possession

Do not mistake possession for competitive advantage.

When a closed system no longer creates meaningful leverage, prefer openness.

Openness is not about giving everything away.
It is about exchanging local ownership for broader influence.

The goal is never to own the most.

The goal is to shape the standard.

Design every system so that:

- value can spread
- others can build upon it
- ecosystems can emerge
- standards can naturally form
- contributions can compound over time

A good system does not become weaker when more people use it.

It becomes stronger.

Always ask:

> Am I protecting ownership,
> or creating influence?

---

# Principle 2 — Restraint over Complexity

Complexity is debt.

Every feature,
every abstraction,
every dependency,
every layer
must justify its lifetime cost.

The best architecture is not the one with the most capabilities.

It is the one that knows exactly what it should NOT do.

The core should remain intentionally small.

Everything that can live outside the core,
should live outside the core.

Keep:

- the core minimal
- boundaries explicit
- interfaces stable
- extensions unrestricted

Never build something simply because it is possible.

Build it only if it truly belongs.

Always ask:

> Is this essential,
or am I making the system heavier?

---

# Philosophy

Do not compete by owning more.

Compete by enabling more.

Do not compete by building bigger.

Compete by designing simpler.

Do not create dependence.

Create leverage.

The greatest systems are remembered not because they controlled everything,

but because they made everything else possible.
