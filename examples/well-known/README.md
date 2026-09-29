# Well-known file templates (Universal Links / App Links)

Templates for the two files a domain must publish so that `https://<host>/...` links open the
app instead of the browser. They are only needed when you set `ASSOCIATED_DOMAINS` in
`app/.env` (see [`app/README.md`](../../app/README.md)); the app does nothing extra otherwise.

| File | Platform | What it is |
|---|---|---|
| [`apple-app-site-association`](./apple-app-site-association) | iOS (Universal Links) | Tells iOS which app IDs may open which paths of the domain. |
| [`assetlinks.json`](./assetlinks.json) | Android (App Links) | Tells Android which package + signing certificate may handle the domain's links. |

Both templates match every path on the host. Replace the placeholders before publishing:

- `TEAMID.com.example.pushshell` in the AASA file: `<Apple Team ID>.<IOS_BUNDLE_ID>`.
- `com.example.pushshell` in `assetlinks.json`: your `ANDROID_PACKAGE`.
- `SHA256_FINGERPRINT` in `assetlinks.json`: the SHA-256 fingerprint of the signing certificate
  (see below). Several fingerprints can be listed, for example the upload key and the Play App
  Signing key.

## Where to host them

For every host in `ASSOCIATED_DOMAINS`:

- `https://<host>/.well-known/apple-app-site-association` (no file extension)
- `https://<host>/.well-known/assetlinks.json`

Requirements for both:

- Served over HTTPS with a valid certificate, `Content-Type: application/json`, status 200.
- **No redirects** (a 301/302 makes verification fail), and no authentication.
- Every host needs its own copy: `example.com` and `www.example.com` are verified separately.

## Finding the values

**Apple Team ID**: [developer.apple.com/account](https://developer.apple.com/account) →
Membership details → Team ID (10 characters). The bundle ID is `IOS_BUNDLE_ID`.

**Android SHA-256 fingerprint**:

- Local / upload keystore:

  ```sh
  keytool -list -v -keystore release.keystore -alias <ANDROID_KEY_ALIAS>
  # use the "SHA256:" line under "Certificate fingerprints"
  ```

- Google Play App Signing: Play Console → your app → Test and release → App integrity →
  App signing → "App signing key certificate" → SHA-256. Apps installed from Play are signed
  with this key, not the upload key, so it must be in `assetlinks.json`.
- The debug keystore (`~/.android/debug.keystore`, alias `androiddebugkey`, password `android`)
  gives a different fingerprint, so add it too if you want to test debug builds.

## Verifying

**Apple**

- Fetch the file yourself and check the headers:
  `curl -sI https://<host>/.well-known/apple-app-site-association`.
- iOS does not fetch the file from your server directly; it goes through Apple's CDN
  (`https://app-site-association.cdn-apple.com/a/v1/<host>`), which can take some time to pick
  up changes. Reinstall the app after the CDN has the new file.
- On a Mac, `swcutil dl -d <host>` (macOS 13+, needs sudo) shows the AASA that the system
  downloads and its status. For development you can add `?mode=developer` to the
  `applinks:<host>` entry and enable Associated Domains Development on the device.
- Tap a link from Notes or Messages (typing the URL into Safari's address bar never opens the
  app).

**Android**

```sh
# Re-run domain verification and read the result
adb shell pm verify-app-links --re-verify com.example.pushshell
adb shell pm get-app-links com.example.pushshell
# each host should show "verified"

# Manual test
adb shell am start -a android.intent.action.VIEW -d "https://example.com/inbox"
```

If a host is not `verified`, check the package name, the fingerprint and that the URL is
reachable without redirects.
