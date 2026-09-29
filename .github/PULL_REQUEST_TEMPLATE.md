<!-- English or Japanese is fine. / 英語・日本語どちらでも構いません。 -->

## Summary

<!-- What does this change and why? Link the issue if there is one (Closes #...). -->

## Changes

-

## Compatibility

<!-- Delete lines that do not apply. -->

- [ ] Bridge protocol (`app/src/bridge.ts`) changed: additive / **breaking**
- [ ] HTTP API (`openapi/openapi.yaml`) changed: additive / **breaking**
- [ ] Database schema changed (migrations for sqlite / mysql / postgres and `backend/worker/migrations` included)
- [ ] New or changed environment variables (documented in `.env.example` and README)

## Checklist

- [ ] `mise run gen` was run and generated code is committed
- [ ] `mise run check` passes locally
- [ ] `mise run backend:lint` passes (Go changes)
- [ ] `cmd/worker` still builds without ent / atlas / kin-openapi (changes under `backend/internal`)
- [ ] Docs updated where behavior changed

## How was this tested?

<!-- Commands run, devices / emulators used, screenshots or logs. -->
