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

The `agent` package does not sandbox tools. Embedders choose the complete tool
set and remain responsible for authorization, credentials, network policy,
filesystem access, and resource limits.

The reference `j-agent` and `j-tui` commands enable the first-party Bash Tool.
It executes with the permissions and environment of the current process. The
tool fixes its working directory, validates its JSON input, removes terminal
control characters, bounds model-visible output, and kills its process group
on timeout or cancellation on Linux and macOS. Those controls are not a
sandbox.

The repository Docker image is the supported isolation boundary for the
reference commands. Operators must expose only the intended `/workspace`,
credentials, network, Linux capabilities, and resource limits. Running a
reference command directly on a host grants model-requested Bash commands the
same host permissions as that process.

J-tui's JSON event mode may contain prompts, tool arguments, command output, and
model diagnostics. Treat it as sensitive local data. J-mem is not implemented;
its storage trust boundary must be documented before it accepts untrusted
input.
