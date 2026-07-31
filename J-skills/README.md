# J-skills

J-skills is an optional Agent Skills consumer for J-agent. It validates
standard `SKILL.md` packages beneath explicitly supplied directories and
exposes one ordinary `skill_read` Tool.

At construction time, only each skill's name and description enter the Tool
description. The model loads the complete `SKILL.md` and UTF-8 text references
or scripts only when needed. Resource paths are confined to the selected skill
directory and each read is limited to 1 MiB. Binary assets remain files for
host-supplied tools rather than being coerced into model text.

```go
catalog, err := skills.Load("/opt/agent-skills", "/workspace/.agents/skills")
if err != nil {
    return err
}
catalog, err = catalog.Select("research", "code-review")
if err != nil {
    return err
}
skillTool, err := catalog.Tool()
if err != nil {
    return err
}
runner, err := agent.New(model, agent.WithTools(skillTool))
```

The package follows the [Agent Skills
specification](https://agentskills.io/specification), including the
`SKILL.md` filename, YAML frontmatter, naming constraints, and progressive
disclosure. Description text is normalized at the loading boundary so valid
YAML folded and block scalars do not fail because their parser representation
contains surrounding whitespace; the normalized description must still be
non-empty and at most 1,024 characters. The loader also replaces the common
`{baseDir}` placeholder when a resource is read for compatibility with existing
Pi skills.

J-skills deliberately does not install packages, auto-search user or project
directories, execute scripts, interpret `allowed-tools`, or define trust
policy. Hosts choose the roots and the other Tools supplied to the Agent.
Hosts that share a large root may use `Select` to expose an exact, auditable
subset without introducing glob or precedence semantics.
