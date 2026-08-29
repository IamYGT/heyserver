# Security Operations Guide

This document describes provider-neutral checks; it does not claim that any
specific installation is hardened. Operators must validate their own observed
state after installation and after every material upgrade.

## Baseline

- Protect `/etc/hserver/hserver.env` and agent token files with root-only access.
- Terminate public TLS at a maintained reverse proxy.
- Enable a host firewall with only explicitly required inbound ports.
- Use effective SSH key-only authentication where operationally appropriate.
- Keep the panel, agent, OS packages, nginx, runtimes, and database engines
  updated through a reversible change process.
- Keep backups on a separate failure domain and test restoration.
- Review audit receipts for every host mutation.

## What the panel measures

The Security score reports observed state rather than installation-specific
assumptions:

- exact active UFW status or an observed restrictive iptables policy;
- active Fail2Ban service state;
- installed, currently valid Let's Encrypt certificates;
- effective `sshd -T` password and keyboard-interactive settings;
- a neutral reminder to review DKIM per configured mail domain.

`not configured`, `unavailable`, and `healthy` are distinct outcomes. A URL,
file name, or package alone is not evidence that an integration is healthy.

## Remote-server boundary

Remote operations use enrolled HServer agents. The hub sends versioned task IDs
and bounded parameters; it does not send arbitrary shell commands. Each agent
advertises its observed and mutable capabilities, keeps executable paths and
deploy arguments local, and refuses undeclared work.

## Release and recovery

Before an upgrade, preserve the current binary and SQLite database. Use the
versioned lifecycle installer so a failed health check restores the previous
binary automatically. Never reset an existing database for a normal release.

Keep production inventories, DNS zone files, incident logs, credentials, and
operator-only runbooks outside the public repository.
