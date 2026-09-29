# Security Policy

## Supported versions

pushshell is pre-1.0. Security fixes are applied to the `main` branch only. If you use the
repository as a template, pull the fix into your copy.

## Reporting a vulnerability

Please **do not** report security issues through public GitHub issues.

Use GitHub's private vulnerability reporting instead:
<https://github.com/nonchan7720/pushshell/security/advisories/new>

Include, where possible:

- which component is affected (`app/`, `backend/` `cmd/server`, `backend/` `cmd/worker`, CI
  workflows) and the commit or version;
- steps to reproduce or a proof of concept;
- the impact you believe it has.

You should receive an acknowledgement within a few days. Once the issue is confirmed, a fix will
be prepared on `main` and the advisory published with credit to the reporter unless you prefer
otherwise.

## Scope notes

Things that are useful to know when assessing an issue:

- `POST /v1/notifications`, `DELETE /v1/logins/{loginId}` and `GET /v1/devices/{id}` are
  protected by `X-API-Key`. Device registration (`POST /v1/devices`) is authorized by the
  pluggable `core.Authorizer`; the default `AllowAll` performs no check and deployments are
  expected to plug in their own verification. Reports about this default being permissive are
  known; reports about ways to bypass a configured authorizer are in scope.
- The WebView only accepts bridge messages and notification URLs from `ALLOWED_ORIGINS`.
  Bypasses of that origin check are in scope.
- Push provider credentials (FCM service account, APNs key) are read from environment
  variables or files and are never sent to clients.
