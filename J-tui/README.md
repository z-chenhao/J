# J-tui

J-tui will be a minimal terminal interface for J-agent.

Its first version should:

- submit text prompts to one J-agent conversation;
- render message and tool lifecycle events;
- show streaming text and explicit failures;
- cancel the active run;
- avoid owning persistence, memory, model routing, or plugin discovery.

J-tui is currently a boundary definition, not an implemented product. New
J-agent APIs should not be added until this consumer demonstrates a concrete
integration failure.

See [the repository design](../docs/design.md).
