# Release signer trust store

`release-signers.json` is the provider-neutral canonical signer identity for the
public installer and signed update manifests. It contains no private material.
The checked-in file remains empty until a canonical Ed25519 release signer is
selected; without an active signer, official tagged publication and staging fail
closed. An empty trust store is never permission to use a checksum-only release
for mutation.

## Schema

```json
{
  "schema_version": 1,
  "signers": [
    {
      "key_id": "lowercase SHA-256 hex of the decoded raw 32-byte public key",
      "public_key": "canonical base64 of the raw 32-byte Ed25519 public key",
      "status": "active"
    }
  ]
}
```

`status` is `active` or `next`. Official CI accepts its private signing secret
only when the derived public key is `active`. Staged installers embed both
`active` and `next` fingerprints so a reviewed overlap can prepare rotation.
The store accepts at most eight unique signers and rejects unknown fields,
duplicate JSON keys, malformed key encodings, and a `key_id` that does not match
`public_key`.

## Runtime mutation gate

An installed panel or managed agent may still expose read-only checksum-only
discovery when its update public-key set is empty. It reports
`signature_status=not_configured`; this is informational metadata, not update
authorization. Panel stage/install and managed-agent upgrade require
`signature_status=verified` from a detached signature checked against a trusted
key. An empty trust store or any other unverified status therefore permits no
update mutation.

This trust store defines no automatic or unattended updater. A verified update
keeps the existing lifecycle rollback boundary: a failed health or service
transition restores the prior lifecycle-owned binary and service state rather
than expanding rollback to unrelated data or configuration.

Validate a proposed store and public key without reading private material:

```bash
./scripts/release-trust.py trust/release-signers.json --require-active
./scripts/release-trust.py trust/release-signers.json \
  --assert-active-key /path/to/release-public-key.b64
```

## Rotation

1. Keep the old signer `active` and add the reviewed replacement as `next`.
2. Publish an old-key-signed transition release that distributes both public
   keys to installed panel and agent update configuration.
3. Promote the replacement to `active` and retain the old key during the stated
   adoption window.
4. Remove the old signer only after that window and preserve an independently
   authenticated recovery copy of the installer digest and new fingerprint.

This Stage-A overlap does not provide cryptographic in-band recovery after an
active signing key compromise. In that case stop publication and updates, issue
a new immutable installer and signer identity through an independent channel,
and require affected operators to re-bootstrap trust. Threshold offline root
recovery belongs to the planned TUF trust layer; it must not be implied by this
single-signature schema.
