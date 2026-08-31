# Mail Integration

Heyserver can manage a configured Stalwart Mail Server without making mail a core
runtime dependency. A fresh installation must show the integration as **not
configured** until working credentials and an endpoint are supplied.

## Configuration

Use the protected Heyserver environment file:

```env
STALWART_URL=
STALWART_API_KEY=
STALWART_ADMIN_USER=
STALWART_ADMIN_PASS=
```

Leave all four values empty when Stalwart is not part of the installation; this
keeps the integration explicitly `not_configured`. Set `STALWART_URL` to the
exact operator-controlled HTTP or HTTPS base URL only after that endpoint is
reachable. Prefer an API key. Basic authentication is a compatibility fallback
and has no default administrator username. Never add real credentials, domains,
public IP addresses, DNS records, or provider account information to this
repository.

## Managed surfaces

The Mail module exposes bounded operations for:

- service status and queue visibility;
- mail domains and accounts;
- aliases and catch-all addresses;
- DKIM key generation and rotation;
- per-domain MX, SPF, DKIM, DMARC, and reverse-DNS assessment.

DNS and DKIM health are evaluated per configured domain. The generic Security
score intentionally does not claim DKIM is active from a URL or a fixed domain.

## Public DNS checklist

For every operator-owned mail domain, verify outside the source tree:

1. MX points only to the intended mail host.
2. The mail host has matching A/AAAA and PTR records.
3. SPF authorizes the actual outbound hosts.
4. DKIM publishes the selector generated for that domain.
5. DMARC policy and reporting addresses match the operator's decision.
6. Submission ports and TLS certificates are tested from an external network.

Use `example.com` and documentation address ranges such as `192.0.2.0/24` in
issues and examples.
