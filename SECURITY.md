# Security policy

## Supported versions

J has not made a stable release. Security fixes are applied to the latest
commit on `main`.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private
security advisory reporting for this repository.

Include:

- affected commit
- reproduction steps
- expected impact
- whether credentials or untrusted tool input are involved

## J-agent trust boundary

J-agent does not sandbox tools. Embedders must authorize tools, validate arguments,
bound outputs, protect credentials, and isolate dangerous execution. The
reference binary does not include a shell tool.

J-mem and J-tui are not implemented yet. Their storage, terminal, and local-file
trust boundaries must be documented before they accept untrusted input.
