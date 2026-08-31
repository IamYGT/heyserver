# Migrating external monitors to Heyserver

This guide describes a provider-neutral migration from an external uptime
monitoring service to Heyserver's native monitoring engine. It intentionally
contains no installation inventory, hostnames, accounts, or credentials.

## 1. Inventory the source

Export only the monitor configuration needed for migration:

- display name and type (`http` or `tcp`)
- URL or hostname and port
- interval, timeout, retry, and accepted-status settings
- TLS checks and alert routing requirements

Keep source databases, session cookies, API tokens, and notification secrets
outside the repository.

## 2. Configure Heyserver notifications

Create the required notification channels and rules in Heyserver before importing
monitors. Send a test notification from each channel and record any provider
limitations separately from monitor health.

## 3. Import monitors

Use the authenticated REST API or the panel UI. For scripted imports, load the
panel URL and token from the operator's environment rather than embedding them:

```python
import os
import requests

panel_url = os.environ["HSERVER_PANEL_URL"].rstrip("/")
token = os.environ["HSERVER_TOKEN"]

monitor = {
    "name": "Example application",
    "type": "http",
    "url": "https://app.example.com/health",
    "intervalSecs": 60,
    "timeoutSecs": 10,
    "acceptedStatusCodes": ["200-299"],
}

response = requests.post(
    f"{panel_url}/api/uptime/monitors",
    headers={"Authorization": f"Bearer {token}"},
    json=monitor,
    timeout=30,
)
response.raise_for_status()
```

Do not print or persist the token in migration output.

## 4. Run both systems in parallel

Keep the existing monitor active while validating Heyserver. Compare at least:

1. HTTP/TCP state transitions.
2. Timeout and retry behavior.
3. TLS expiry warnings.
4. Failure and recovery notifications.
5. Public status-page visibility, when used.

Choose the parallel-run duration according to the installation's check interval
and operational risk. A route import succeeding is not proof that alert delivery
or failure detection works.

## 5. Cut over

After the acceptance checks pass:

1. Export a final copy of the source configuration.
2. Disable the old monitors without deleting their data.
3. Observe one more Heyserver check cycle and notification test.
4. Remove the old service, proxy, DNS, and files only through the source
   product's documented uninstall path and a separately approved cleanup scope.

## Rollback

If Heyserver monitoring has a critical issue, re-enable the old monitors, keep the
Heyserver monitor definitions for diagnosis, and avoid deleting either history
until the incident is understood.

## Acceptance criteria

- Every intended monitor exists once in Heyserver.
- Scheduled checks run at the configured interval.
- DOWN and recovery transitions create the expected events.
- Notification delivery succeeds through every required channel.
- Public status pages expose only explicitly selected monitors.
- No source credential or installation inventory enters Git.
