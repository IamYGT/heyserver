# Docker Compose Deployments

Heyserver can manage a local Git checkout as a first-class Docker Compose deploy
target. This mode is intentionally different from a script target: the panel
stores deployment intent and assembles a fixed Compose command instead of
accepting an arbitrary command string.

Managed servers use the separate, capability-scoped agent deploy-plan contract.
The local Compose workflow never runs a local command while claiming to manage
a remote node.

## Requirements

- Heyserver must be installed natively on the host that owns the project.
- `git` must be able to read the configured checkout.
- Docker Engine and the Docker Compose v2 plugin must be available to the
  Heyserver service identity.
- The project directory must be an absolute path. It may contain an existing
  Git checkout, be empty, or be absent. Empty and absent targets are cloned on
  their first deployment from the configured repository URL.
- The Compose file may be left empty for Docker Compose auto-discovery, or it
  may be a relative path contained by the project directory. Absolute paths and
  `..` traversal are rejected.

Keep repository credentials in the host's SSH or Git credential mechanism. Do
not embed access tokens in the repository URL, Compose file, deploy logs, or a
committed environment file. Runtime `.env` and Docker secret material remain
installation-owned.

## Add a target

1. Open **Deploy** and select **Add Target**.
2. Choose **Docker Compose**.
3. Enter the repository display URL, branch, and absolute project directory.
4. Leave **Compose File** empty to use auto-discovery, or enter a contained
   relative path such as `deploy/compose.yaml`.
5. Save the target and run **Deploy Preflight**. A missing checkout is shown as
   pending provisioning rather than falsely reported as an existing checkout.

The manual deploy and rollback buttons remain disabled until a fresh preflight
succeeds. Webhook and API-triggered operations also repeat preflight on the
server immediately before a run is queued, so bypassing the UI cannot bypass
readiness checks.

## Create an isolated staging environment

An administrator can select **Create Staging Environment** on a production
target. Heyserver derives repository and executor intent while requiring a
separate absolute project directory and recording the production target as the
staging source. Name and branch remain staging-owned choices.

The operation deliberately does not clone production runtime state:

- project environment values remain unconfigured;
- webhook signing material is not copied and auto-deploy starts disabled;
- project domains, certificates, and DNS state remain unconfigured;
- the staging directory cannot equal, contain, or sit inside another deploy
  target or `${HSERVER_DATA_DIR}`, including through an existing symlink.

A staging target cannot produce another staging target. The production target
cannot be deleted until its staging children are removed, and later target
updates cannot cross a staging storage boundary. Configure staging-specific
environment values, webhook delivery, domains, TLS, and DNS independently only
after creation.

The same lifecycle is available to automation:

```bash
hserverctl deploy staging create --confirm \
  --name "App Staging" \
  --branch develop \
  --project-dir /srv/apps/app-staging \
  PRODUCTION_TARGET_ID
```

## Reusable installation templates

Administrators can define reusable executor presets as JSON files below the
fixed `${HSERVER_DATA_DIR}/deploy-templates` directory. The add-target dialog
loads this read-only inventory and applies only the branch, deployment kind,
Compose file, and deployment script. It never replaces the target name,
repository URL, project directory, webhook provider, or signing secret.

The public source tree and release archive include Compose and Node.js examples.
The native lifecycle installer seeds both only when the template directory does
not already exist; upgrades and existing template directories are left
unchanged. A schema-v1 Compose template is:

```json
{
  "schemaVersion": 1,
  "id": "docker-compose",
  "name": "Docker Compose",
  "description": "Deploy with Heyserver's fixed Docker Compose lifecycle.",
  "branch": "main",
  "deploymentKind": "compose",
  "composeFile": "",
  "deployScript": ""
}
```

The filename must be `docker-compose.json` because `id` must match its lowercase
basename. Compose templates may set a contained relative `composeFile` but
cannot define `deployScript`. Script templates require a deployment script,
cannot define `composeFile`, and pass the same command validation as a manually
configured script target. Unknown fields are rejected, so a template cannot
smuggle a repository URL, project directory, credential, or webhook secret into
the target form.

Heyserver accepts at most 128 regular `.json` files of 64 KiB each. Symlinks and
group- or world-writable directories or files are rejected. Inventory status is
`not_configured` when the directory has no templates, `healthy` when every
template is valid, and `unavailable` when the directory cannot be read or one or
more files are unsafe or invalid. In the last state, valid templates remain
available and each rejected basename is reported with a bounded reason.

## Signed deployment webhooks

The add-target dialog supports explicit **GitHub** and **GitLab** provider
contracts. The public URL is the same for either provider:

```text
https://panel.example.com/api/deploy/webhook/TARGET_ID
```

For GitHub, configure JSON delivery, choose push events, and use the same secret
entered in Heyserver. Heyserver validates `X-Hub-Signature-256` over the exact body
and requires the unique `X-GitHub-Delivery` for every push.

For GitLab, use **Standard Webhooks**, select push events, and store the returned
`whsec_` signing token in Heyserver. Heyserver validates `webhook-id`,
`webhook-timestamp`, and any matching `v1,` entry in `webhook-signature`; the
legacy plaintext `X-Gitlab-Token` contract is intentionally not accepted for a
new target. See the upstream [GitHub validation guide](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries) and [GitLab webhook documentation](https://docs.gitlab.com/user/project/integrations/webhooks/) for provider-side setup.

Authenticated push delivery identities are stored before target eligibility is
evaluated. A provider retry therefore receives HTTP 200 but cannot enqueue the
same push again. The replay lock survives panel restarts. Signing values live in
installation-owned mode-`0600` files under:

```text
${HSERVER_DATA_DIR}/deploy-webhook-secrets/target-ID.secret
```

The panel reports `not configured`, `healthy`, and `unavailable` separately. A
missing or unreadable signing file does not disable manual deployment, but the
public webhook fails closed until the file is restored or the target is updated.

## Readiness contract

Preflight is read-only and reports each boundary separately:

- target enabled state;
- project-directory availability;
- readable Git checkout;
- or, for a first deployment, a valid token-free HTTPS/SSH repository URL and
  a reachable parent directory;
- Docker Compose v2 availability;
- `docker compose config --quiet` validation.

An unavailable Docker installation and an invalid Compose configuration are
reported as failed checks, not as a healthy or empty deployment target.
Checks that can only run after the first clone use the distinct `pending`
status. Pending provisioning remains deploy-eligible; failed checks do not.

## Revision comparison

Select **Compare Deployment Revisions** on a Deploy card, or run:

```bash
hserverctl deploy revision TARGET_ID
```

Heyserver reads the exact local `HEAD`, the newest successful deployment commit,
and the commit the rollback endpoint would currently select. It reports whether
the checkout matches the latest recorded deployment, whether tracked files
have local changes, commit distance from the rollback revision, and a bounded
file/insertion/deletion summary. The three states are intentionally distinct:

- `not_deployed`: the project checkout has not been provisioned;
- `ready`: local Git state and any available rollback comparison were read;
- `unavailable`: the path, checkout, tracked-change state, or stored rollback
  revision could not be read from the local repository.

Revision comparison is local and read-only. It never runs `git fetch`, changes
the checkout, or claims that the configured remote contains a newer revision.
Use preflight immediately before a deploy or rollback; both mutations repeat
their server-side readiness checks.

## Fixed execution contract

For a missing or empty project directory, the first deployment uses this fixed
clone shape:

```text
git clone --branch BRANCH --single-branch -- REPOSITORY_URL PROJECT_DIRECTORY
```

Existing checkouts are updated without an implicit merge:

```text
git pull --ff-only origin BRANCH
```

After the checkout is ready, Heyserver executes only these Compose operations
from the configured project directory:

```text
docker compose [-f RELATIVE_FILE] config --quiet
docker compose [-f RELATIVE_FILE] up -d --build --remove-orphans
```

The optional `-f` value comes from the validated relative Compose-file field.
The API cannot inject another executable or additional arguments into a Compose
target. Operators who deliberately need a custom local workflow can use the
separate **Script** target type; remote custom workflows must be installed as
fixed agent plans on the managed node.

## Project services

After a Compose target has been deployed, open **Project Services** on its
Deploy card to inspect the containers observed for that specific project. The
panel shows the stable Compose service key, replica/container name, image,
state, health, exit code, and published ports. Inventory is read with:

```text
docker compose [-f RELATIVE_FILE] ps --all --format json
```

Service controls remain inside the same persisted project directory and expose
only fixed operations:

```text
docker compose [-f RELATIVE_FILE] start SERVICE
docker compose [-f RELATIVE_FILE] stop SERVICE
docker compose [-f RELATIVE_FILE] restart SERVICE
docker compose [-f RELATIVE_FILE] up -d --build --no-deps SERVICE
```

The last command is shown as **Recreate**. There is no arbitrary command or
project-wide `down` endpoint. Service names are validated before reaching the
CLI. Mutations require manager or admin access and use the shared mutation rate
limit.

Project-service logs are timestamped, default to the latest 200 lines, accept
at most 1000 lines, and cap the response at 1 MiB. Truncation is reported in
both the API envelope and the UI instead of silently returning an incomplete
response.

## Project environment

Admins can open **Project Environment** on a Compose target and store or replace
one variable at a time. Values are write-only: API and browser responses return
only sorted variable names, never current values. Selecting an existing key
prepares a replacement without loading its stored value into the page.

The generated Compose env file lives under:

```text
${HSERVER_DATA_DIR}/deploy-env/target-ID.env
```

The directory is forced to mode `0700`; files are atomically replaced with mode
`0600`. They remain outside the Git checkout and are excluded from portable
configuration and public-source exports. Removing the last variable removes the
file. Deleting a deploy target transactionally stages and removes its associated
environment file instead of leaving an orphaned secret.

When present, the same installation-owned file is passed to preflight, deploy,
rollback, service inventory, service logs, and service actions:

```text
docker compose --env-file ${HSERVER_DATA_DIR}/deploy-env/target-ID.env ...
```

Keys follow portable environment-name syntax. Values may be empty and may use
spaces, `#`, `$`, and other literal characters, but line breaks, NUL bytes, and
single quotes are rejected because the generated Compose env format uses one
literal single-quoted value per line. Each value is limited to 64 KiB.

## Project domains

Open **Project Domains** on a Compose target to bind a public hostname to one
explicitly published host port. The service label records which Compose service
owns the mapping; the network boundary is always constructed by the server as:

```text
http://127.0.0.1:HOST_PORT
```

The API never accepts a raw upstream URL. Use the published host port shown in
**Project Services**, not a container-only port. This keeps native Nginx outside
the Compose network while preventing a browser request from selecting another
host or scheme.

Heyserver writes `<domain>.conf` below `HSERVER_NGINX_SITES_AVAILABLE`, links it
below `HSERVER_NGINX_SITES_ENABLED`, tests the complete configuration with
`nginx -t`, and reloads Nginx. A failed test or reload removes the staged files
and restores the previous Nginx state. Deletion verifies Heyserver's target and
domain ownership markers before it moves either file, then uses the same test,
reload, and rollback sequence. A deploy target with active mappings cannot be
deleted until those mappings are removed.

### Managed TLS

Admins can select **Configure TLS** for a mapping after its public DNS points to
the host and inbound port 80 reaches Nginx. Heyserver uses a fixed Certbot
HTTP-01 webroot command; the browser supplies only an optional ACME account
email. The domain, certificate name, challenge root, config root, authenticator,
and deploy hook are assembled from the persisted mapping and installation
configuration rather than request data.

The HTTP virtual host always serves `/.well-known/acme-challenge/` from
`HSERVER_ACME_WEBROOT`. After Certbot succeeds, Heyserver parses the issued X.509
certificate, verifies that it covers the mapped hostname, then atomically
replaces the owned Nginx file with:

- an HTTP challenge location plus redirect for all other requests;
- an HTTPS reverse proxy to the same fixed loopback upstream;
- the certificate and key below `HSERVER_CERTBOT_CONFIG_DIR/live/DOMAIN`;
- TLS 1.2 and TLS 1.3 only.

The complete Nginx configuration is tested and reloaded. A failed test or
reload restores the previous HTTP configuration. The database records desired
TLS state only after the host transition succeeds. Certificate health remains
observed from the actual certificate file and is reported as `healthy`,
`expiring`, `expired`, or `unavailable`; a database flag or file path alone is
not treated as valid TLS. Disabling TLS restores HTTP but deliberately preserves
the certificate files instead of revoking or deleting them.

Certbot stores the renewal lineage in its configured state directory. Heyserver
runs one maintenance pass at panel startup and every 12 hours afterwards. Only
observed `expiring` or `expired` mappings reach the fixed `certbot renew`
operation; healthy certificates are not touched, and a missing certificate is
left `unavailable` rather than silently reissued without the operator's ACME
account decision. The fixed deploy hook reloads Nginx after a successful
renewal. Distribution-provided Certbot timers may coexist, but Heyserver does not
depend on one being enabled for project-domain maintenance.

Use **Probe** for an independent on-demand, three-second loopback application
check. Redirect following is disabled, and probe results distinguish:

- `healthy`: HTTP 200-399;
- `unhealthy`: the upstream answered outside that range;
- `unavailable`: no HTTP response was observed.

This probe proves the local application upstream only. It does not claim that
public DNS, an external firewall, or TLS is ready.

## Rollback behavior

Each successful run records the Git commit that was active before its pull.
The initial clone has no previous local commit and therefore does not create a
rollback revision by itself.
Rollback checks out the latest available previous commit, re-runs the same
Compose config validation, and reconciles the project with the same fixed
`up -d --build --remove-orphans` command. Deployment logs record the result.
An invalid Compose file in the current commit does not disable rollback: host,
Git, and Docker Compose availability must still pass, then configuration is
validated again after the previous commit is checked out.

Rollback changes application code and containers; it does not rewind external
databases, named volumes, bind-mounted state, or third-party services. Use
application-specific backup and restore procedures for those resources.

## API

- `GET /api/deploy/templates` returns the admin-only installation template inventory.
- `GET /api/deploy/targets/{id}/preflight` returns the current readiness report.
- `GET /api/deploy/targets/{id}/services` returns the observed Compose services.
- `GET /api/deploy/targets/{id}/services/{service}/logs` returns bounded service logs.
- `POST /api/deploy/targets/{id}/services/{service}/{action}` runs `start`,
  `stop`, `restart`, or `recreate` through fixed arguments.
- `GET /api/deploy/targets/{id}/environment` returns names only.
- `PUT /api/deploy/targets/{id}/environment` writes or replaces one value.
- `DELETE /api/deploy/targets/{id}/environment/{key}` removes one value.
- `GET /api/deploy/targets/{id}/domains` lists project-domain mappings.
- `POST /api/deploy/targets/{id}/domains` activates one HTTP mapping.
- `GET /api/deploy/targets/{id}/domains/{domainId}/health` probes its loopback upstream.
- `POST /api/deploy/targets/{id}/domains/{domainId}/tls` obtains and activates managed TLS.
- `DELETE /api/deploy/targets/{id}/domains/{domainId}/tls` restores HTTP without deleting certificate files.
- `DELETE /api/deploy/targets/{id}/domains/{domainId}` removes one owned mapping.
- `POST /api/deploy/manual/{targetId}` repeats preflight and queues a deploy.
- `POST /api/deploy/rollback/{targetId}` repeats preflight and queues rollback.
- `GET /api/deploy/history` and the run-log endpoint expose recorded outcomes.

The generated OpenAPI contract and the in-panel **Developer API** explorer are
the authoritative route inventory.
