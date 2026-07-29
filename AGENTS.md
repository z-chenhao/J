# Architectural Constitution

> Build systems that outlive their implementations.

# Mission (J)

J is a repository of small, composable agent projects.

- `J-agent` is the minimal Go model/tool runtime.
- `J-tui` is a terminal consumer of J-agent.
- `J-mcp` projects MCP servers into ordinary J-agent tools.
- `J-mem` is optional local transcript and long-term memory.
- `J-packages` installs explicit MCP and Agent Skills packages for product hosts.
- `J-skills` loads standard Agent Skills progressively.
- `J-subagents` runs isolated foreground child Agents through a tool.

J-agent must not depend on sibling product modules. The siblings validate the
same public seams available to external consumers. They are examples, not
privileged framework layers.

Repository-level architecture and accumulated decisions live in
`docs/design.md`. Component protocol details stay with the component that owns
them.

Core invariants are explicit. Experimental contracts become stable only after
real consumers validate them. Extra capabilities stay outside J-agent unless a
concrete integration proves that a smaller composition is insufficient.

J-Space and Jacobian-lens work belongs under `J-agent/research/` and is not a runtime
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
