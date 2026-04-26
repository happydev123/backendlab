# Egress policy (server mode)

Runs executed by `backendlab serve` and `backendlab worker` send HTTP requests from a shared
machine on behalf of whoever submitted the run. The `egress` section of the server configuration
limits where those requests may go. The CLI does not apply it: a person running `backendlab` on
their own machine can already reach anything that machine can reach.

```yaml
egress:
  allow_hosts:
    - api.staging.example.com
    - "*.svc.example.net"
    - reports.example.org:8443
  allow_cidrs:
    - 10.40.0.0/16        # internal upstreams, explicit operator opt-in
  allow_loopback: false   # true only for local development
```

## What is checked

Every outbound request in server mode passes three checks.

1. **URL check** (`CheckURL`), before the request is sent and again for every redirect target
   (`CheckRedirect`):
   - the scheme must be `http` or `https`;
   - the URL must not contain userinfo (`user:pass@host`); credentials belong in target headers
     that read environment variables;
   - when `allow_hosts` is set, the host must match an entry.
2. **Host allowlist at dial time**, repeating the host match so a transport that bypasses the URL
   check still cannot connect elsewhere.
3. **Address check** (`CheckIP`) on every address the host resolves to, or on the IP literal in
   the URL.

### Host matching

- `api.example.com` matches that name only. `*.example.com` matches any name with at least one
  more label (`a.example.com`, `a.b.example.com`) but not `example.com` itself.
- `host:port` restricts the entry to one port. Without a port, any port matches. URLs without an
  explicit port use 80 for `http` and 443 for `https`.
- Comparison is case-insensitive and ignores one trailing dot (`API.Example.com.` is
  `api.example.com`).
- Host names must be ASCII letters, digits, `-` and `_`. International names must be written in
  punycode (`xn--...`), in both the configuration and the target URL.
- Names whose last label is numeric (`2130706433`, `127.1`, `0x7f.1`) are refused. Some system
  resolvers read these as IPv4 addresses, so they are a common way to disguise an internal
  address.
- An IP literal in a URL is matched only by an identical IP entry in `allow_hosts`; wildcard
  entries never match IP literals. It must also pass the address check.
- An empty `allow_hosts` places no restriction on names; the address check still applies to every
  destination.

### Address classes

Refused unless explicitly allowed:

| Range | Class |
| --- | --- |
| `127.0.0.0/8`, `::1` | loopback (allowed only by `allow_loopback: true`) |
| `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` | private (RFC 1918) |
| `100.64.0.0/10` | shared address space (CGNAT) |
| `169.254.0.0/16`, `fe80::/10` | link-local |
| `fc00::/7` | IPv6 unique local |
| `fec0::/10` | deprecated IPv6 site-local |
| `192.0.0.0/24`, `192.88.99.0/24`, `198.18.0.0/15`, `240.0.0.0/4`, `100::/64`, `::ffff:0:0:0/96` | reserved |
| `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32` | documentation |
| `::/96` | deprecated IPv4-compatible IPv6 |
| `2001::/32` | Teredo |
| `64:ff9b:1::/48` | local-use NAT64 |

Refused always, even inside `allow_cidrs`:

| Range | Class |
| --- | --- |
| `0.0.0.0/8`, `::` | unspecified ("this network"; `0.0.0.0` reaches the local host on many systems) |
| `169.254.169.254`, `fd00:ec2::254` | cloud metadata services |
| `224.0.0.0/4`, `ff00::/8` | multicast |
| `255.255.255.255` | broadcast |
| addresses with an IPv6 zone (`fe80::1%eth0`) | zoned |

`allow_cidrs` opens the first table for the listed ranges. Loopback is not opened by
`allow_cidrs`; use `allow_loopback`. A broad entry such as `0.0.0.0/0` opens every private IPv4
range and should be avoided.

### IPv6 forms

- **IPv4-mapped** addresses (`::ffff:127.0.0.1`, also written `::ffff:7f00:1`) are converted to
  the IPv4 address before any check, so they are treated exactly like `127.0.0.1`. Resolvers can
  return mapped forms, and dual-stack sockets connect to the IPv4 address.
- **IPv4-compatible** addresses (`::a.b.c.d`) are deprecated (RFC 4291) and refused outright.
- **NAT64** addresses in the well-known prefix `64:ff9b::/96` (RFC 6052) carry an IPv4 address in
  their last 32 bits, and a NAT64 gateway forwards traffic to that IPv4 address. BackendLab checks
  the embedded address with the same rules and refuses the IPv6 address if the embedded one would
  be refused, so `64:ff9b::a00:1` (10.0.0.1) is refused while `64:ff9b::808:808` (8.8.8.8) is
  allowed. An operator who lists the NAT64 prefix itself in `allow_cidrs` takes responsibility for
  what the gateway can reach. The local-use prefix `64:ff9b:1::/48` (RFC 8215) is refused.
- **6to4** addresses (`2002::/16`) embed an IPv4 address in bits 16 to 47 and get the same
  treatment as NAT64.
- **Teredo** addresses (`2001::/32`) embed obfuscated server and client addresses and are refused.

## DNS rebinding

A check on a host name is only meaningful if the connection goes to the address that was checked.
An attacker who controls a DNS zone can answer the first lookup with a public address and the next
one with `127.0.0.1` (DNS rebinding), or return a mix of public and internal addresses and wait
for the client to fall back to the internal one.

`Policy.DialContext` closes both gaps for the transports that use it:

- it resolves the host itself, exactly once per connection attempt;
- it checks every resolved address and refuses the whole answer if any address is refused, so a
  mixed answer cannot be used;
- it dials the checked address as an IP literal, so no second resolution happens between the
  check and the connection.

Limits of this approach, stated plainly:

- A long-lived keep-alive connection stays connected to the address checked when it was opened.
  That is the intended behavior, not a gap, but it means a policy change applies only to new
  connections.
- Addresses are tried in resolver order, one at a time. There is no parallel IPv4/IPv6 attempt
  ("happy eyeballs"), so a host whose first address is unreachable connects more slowly.
- At most 32 resolved addresses are considered per dial.
- TLS still verifies the certificate against the host name, so dialing the IP literal does not
  weaken certificate checks.

## Proxies

Server-mode transports set `Proxy` to nil and do not read `HTTP_PROXY`, `HTTPS_PROXY` or
`NO_PROXY`. With a proxy, the server connects to the proxy and the proxy resolves and connects to
the upstream. The dial-time address checks would then apply only to the proxy's address, and the
policy could not see or control where requests actually go. An environment variable set for
unrelated reasons on the host would silently change what the egress policy means.

Operators who need outbound traffic to go through a proxy should enforce destinations at that
proxy and at the network layer; BackendLab's checks do not extend through it.

## Application checks do not replace network controls

The policy reduces what a submitted run can reach, but it runs inside the same process as
everything else and only covers requests made through transports configured with it. It cannot
protect against:

- a code path or dependency that creates its own HTTP client;
- traffic that is not HTTP, or a future feature that forgets to use the policy;
- infrastructure behavior it cannot see, such as a NAT64 gateway, a service mesh sidecar or
  routing rules that map a public-looking address to an internal service.

Deploy the server where the network itself restricts egress: a dedicated subnet or namespace,
firewall or security-group rules that block internal ranges and the metadata endpoint, and cloud
metadata configured to require session tokens (for example IMDSv2 with a hop limit of 1). Treat
the application policy as a second layer that makes mistakes in the first one less likely to be
exploitable, not as the boundary.
