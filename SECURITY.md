# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| v0.1.x  | :white_check_mark: |
| < v0.1  | :x:                |

## Reporting a Vulnerability

**Please do NOT file public issues for security vulnerabilities.**

Send a private email to the maintainer (see [CODEOWNERS](CODEOWNERS)) with:

1. A clear description of the vulnerability.
2. Steps to reproduce (or a proof-of-concept).
3. The impact assessment (what an attacker could achieve).
4. Your name / handle (optional, for credit).

We aim to **acknowledge within 48 hours** and provide a fix or mitigation plan within 14 days for critical issues.

## Scope

In-scope vulnerabilities include but are not limited to:

- Balance manipulation bypassing Transfer/Burn checks
- Race conditions in MarketMaker settle flow
- Plugin loader code execution (e.g., malicious YAML/JSON in plugin config)
- OpenRTB adapter field injection
- Admin Console auth bypass (if/when auth is added)

Out-of-scope:

- Denial-of-service via spam (Phase 1 has no rate limiting; please add `WARN` in your report)
- Phishing / social engineering
- Vulnerabilities in third-party dependencies (file upstream)

## Disclosure Policy

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure):

1. Reporter emails the maintainer privately.
2. Maintainer confirms and starts working on a fix.
3. Fix is prepared on a private branch.
4. Once a fix is ready, we coordinate a public release date.
5. CVE is requested (if applicable).
6. Public disclosure + credit to reporter (if they consent).
