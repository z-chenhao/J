# J Packages

J Packages is the experimental, construction-time package host for J products.
It installs explicit packages, validates their manifests, and projects only two
standard contribution types:

- stdio MCP servers as ordinary `agent.Tool` values;
- standard Agent Skills roots.

It does not add a Plugin interface to J-agent. Product hosts decide whether to
load packages and how package Tools and Skills participate in their product.

## Install

```sh
go install github.com/z-chenhao/J/J-packages/cmd/j@latest
j --version
```

The J-tui release installer installs both `j-tui` and `j`.

## Use

```sh
j package check ./my-package
j package add ./my-package
j package add git:https://github.com/example/my-package.git@v1.0.0
j package list
j package doctor
j package update dev.example.package
j package remove dev.example.package
```

Local packages remain linked to their explicit directory. Git packages require
an explicit tag, branch, or commit and are checked out into an immutable
commit-addressed cache. Update is explicit. Removal unregisters the package but
retains cached source so it is recoverable.

The private registry defaults to `~/.j/packages.json`; Git checkouts default to
`~/.j/packages/`. `J_PACKAGES_REGISTRY` and `J_PACKAGES_CACHE` override these
paths for the `j` command.

## Embed

Another J-agent product can consume the same installed packages without
depending on J-tui:

```go
session, err := packages.Open(ctx, packages.HostConfig{
    RegistryPath: registryPath,
})
if err != nil {
    return err
}
defer session.Close()

runner, err := agent.New(model, agent.WithTools(session.Tools()...))
```

`Session.SkillRoots()` returns validated absolute roots for a host that also
uses J-skills. Package Tool name conflicts remain a product-host decision.

See [the complete package protocol](../docs/packages.md).
