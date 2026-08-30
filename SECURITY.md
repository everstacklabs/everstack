# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Everstack, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email security@everstack.ai with:

- A description of the vulnerability
- Steps to reproduce
- Potential impact
- Any suggested fixes

We will acknowledge your report within 48 hours and provide a timeline for a fix.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| Latest  | Yes                |

## Security Best Practices

When deploying Everstack:

- Always use TLS in production.
- Rotate API keys regularly.
- Keep your instance updated to the latest supported version.
- Use strong, unique database and API-key hash secrets.
- Enable built-in authentication or OIDC before exposing the dashboard. The
  local quickstart intentionally sets `EVS_AUTH_MODE=none` and is not a
  production security profile.
