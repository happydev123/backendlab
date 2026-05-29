# Security policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub's
"Report a vulnerability" form on this repository rather than in a public
issue. Include the version (`backendlab version`), the command or endpoint
involved, and a minimal reproduction. You should receive an acknowledgement
within a week.

## Supported versions

BackendLab has not had a tagged release yet. Fixes are made on the main
branch.

## Scope

In scope:

- Credential or secret disclosure in reports, logs, the database or the
  dashboard.
- Bypasses of the write gate (`--allow-writes`, `allow_writes` in server
  configuration).
- Server-mode requests reaching destinations outside the configured egress
  policy.
- Authentication, session, CSRF or login-throttling weaknesses in server mode.
- Injection through report rendering (terminal escape sequences, Markdown,
  HTML in the dashboard).
- Suite or rules files causing file reads outside their directory in server
  mode, or unbounded resource use.

Out of scope:

- The demonstration service (`cmd/mockservice`), which is a development tool
  with deliberate faults and must never be exposed.
- Destinations the local CLI user chooses to target; the CLI acts with the
  invoking user's authority.
- Multi-tenant isolation, which the first release does not provide.

See [docs/security.md](docs/security.md) for the threat model and the
limits of redaction and egress filtering.
