# Roadmap

English | [日本語](ROADMAP.ja.md)

This is the list of things pushshell should grow into, in rough priority order. It is a living
document: items move around as issues come in. If you want to work on one, open an issue (or
comment on the existing one) so we can agree on the shape before you start. Items with no
tracking issue yet are up for grabs.

Status legend: ✅ done · 🚧 in progress · ⬜ not started

## Top five

These are the changes with the best ratio of adoption gained to effort spent.

1. ✅ **English README and OSS basics.** English `README.md` as the primary document with the
   Japanese one at `README.ja.md`, plus `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`,
   issue and PR templates and Dependabot. Remaining: English versions of `app/README.md` and
   `backend/README.md`.
2. ⬜ **Web SDK on npm** (working name `@pushshell/web`). Today the web app calls
   `window.ReactNativeWebView.postMessage` directly and listens for the `nativeapp` CustomEvent
   itself. A thin typed wrapper generated from `app/src/bridge.ts` should offer
   `pushshell.login(id, token)` returning a Promise that resolves on `registered` and rejects on
   `error`, an `isNative()` check, and typed event subscriptions. Add `protocolVersion` to the
   `ready` event at the same time so compatibility can be managed later.
3. ✅ **Universal Links / App Links.** An `https://example.com/...` link from email or social
   media now launches the app and opens in the WebView. `ASSOCIATED_DOMAINS` (opt-in, from env)
   configures `ios.associatedDomains` and the Android `intentFilters`, and every associated domain
   is added to `ALLOWED_ORIGINS` automatically. Templates and publishing instructions for
   `apple-app-site-association` and `assetlinks.json` live in `examples/well-known/`.
4. ⬜ **Richer notification targeting and options.** Only `loginIds` can be targeted today, and
   there is no TTL / priority / collapse key / image / silent (data-only) message / iOS
   `threadId` / `interruptionLevel`. Add direct `installationIds`, filters by platform and
   locale, broadcast to all devices, and topic subscriptions.
5. ⬜ **Web Push (VAPID) as a third platform.** Add `web` to `Platform` and a Web Push
   `push.Sender`, so the same `loginId` API reaches a PWA and the native apps alike.

## App (`app/`)

- ⬜ **Navigation to other origins.** Requests outside `originWhitelist` are simply refused.
  Use `onShouldStartLoadWithRequest` to open other origins in `expo-web-browser`
  (SFSafariViewController / Custom Tabs), handle `target=_blank` windows, and make it easy to
  allow-list OAuth callback origins.
- ⬜ **WebView permissions.** File upload, camera, geolocation and microphone need runtime
  permission handling on Android and usage strings in `Info.plist` on iOS. Make them opt-in
  through env variables so the "no code changes" promise holds.
- ⬜ **More bridge messages.** Web → native: `requestPushPermission` (many apps want to ask
  separately from login), `openSettings` when permission is denied, `setBadge` / `clearBadge`,
  `share`, `haptic`. Native → web: `appState` (foreground / background) and
  `pushPermissionChanged`.
- ⬜ **i18n for native UI strings.** "読み込みに失敗しました" / "再読み込み" etc. are hard-coded
  Japanese. Ship at least `en` and `ja` via `expo-localization`.
- ⬜ **Notification icon, color and channels from env.** Android small icon and accent color,
  and multiple channels (e.g. announcements vs. messages) configurable from `app.config.ts`.
- ⬜ **Cookies and session persistence.** Document `sharedCookiesEnabled` and how login survives
  restarts, and how that interacts with `loginIds` in the `ready` event.
- ⬜ **Tests.** The app is only type-checked in CI. `bridge.ts` and `installation.ts` are mostly
  pure functions and easy to cover with Jest; Maestro can ride on the existing emulator workflow
  for end-to-end coverage. An iOS build job on a macOS runner is also missing.
- ⬜ **OTA updates with `expo-updates`** (optional, but welcome by template users).

## Backend (`backend/`)

- ⬜ **Expo push receipts.** Expo returns tickets synchronously; `DeviceNotRegistered` is only
  reported when receipts are fetched later. Without a receipt job, stale tokens are not cleaned
  up on the Expo path.
- ⬜ **Asynchronous sending and job status.** Sending to 1000 `loginIds` synchronously risks
  HTTP timeouts. Return `202` with a `notificationId`, expose
  `GET /v1/notifications/{id}`, retry 429 / 5xx with backoff, and honor an `Idempotency-Key`
  header.
- ⬜ **Delivery history and admin API.** `GET /v1/logins/{loginId}/devices`, device listing and a
  delivery log. Today the only way to answer "why did this not arrive?" is the process log.
- ⬜ **Configurable `Authorizer` implementations.** The README asks deployments to implement
  `core.Authorizer` themselves. Ship a JWT verifier (JWKS URL) and an `AUTH_VERIFY_URL`
  callback to the web app backend so most users never write Go.
- ⬜ **Multiple apps / tenants.** Several API keys, each scoped to an app, so one backend can
  serve several services. Store keys hashed and support rotation.
- ⬜ **Webhooks.** Notify the web app backend when a device is removed because its token expired,
  or when a registration happens, to complete the two-way integration.
- ⬜ **Operations.** Rate limiting, CORS configuration, `/readyz` with a DB check, Prometheus /
  OpenTelemetry metrics, pruning stale devices via `last_seen_at`, and deduplicating when the same
  push token reappears under a new `installationId` after a reinstall.
- ⬜ **Distribution.** `Dockerfile` and a GHCR image, a `docker compose up` dev environment,
  goreleaser binaries, and deploy templates for Fly.io / Railway / Render. Cloudflare Workers
  support already exists and is a differentiator worth highlighting.
- ⬜ **Client SDKs and CLI.** Generate Go / TypeScript / Python clients from the OpenAPI spec and
  publish them, plus a `pushshell send --login user-1 --title ...` CLI for quick checks and demos.

## Project and community

- ⬜ **Hosted demo.** A public demo web page and downloadable APK, plus a short GIF in the README.
- ⬜ **Positioning.** A comparison with Median / GoNative, Capacitor, Hotwire Native and
  OneSignal, and what pushshell does differently.
- ⬜ **Integration guides per web framework.** Next.js, Rails, Laravel, Django snippets reachable
  from the README.
- ⬜ **Release automation.** release-please or similar, tags, and a `CHANGELOG.md`, so releases
  are cheap once contributors arrive.
- ⬜ **English `app/README.md` and `backend/README.md`.**
- ⬜ **Labels.** `good first issue` and `help wanted` on the tracker, mapped to items here.
