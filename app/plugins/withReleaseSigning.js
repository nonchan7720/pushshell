// Expo config plugin: release ビルドの署名を環境変数で差し替える。
//
// Expo のテンプレートは release も debug keystore で署名する。配布用 APK を作る場合は
// 次の env を与えると android/app/build.gradle の signingConfigs.release を書き換える。
//   ANDROID_KEYSTORE_PATH      keystore ファイルのパス (android/app からの相対 or 絶対)
//   ANDROID_KEYSTORE_PASSWORD  keystore のパスワード
//   ANDROID_KEY_ALIAS          鍵のエイリアス
//   ANDROID_KEY_PASSWORD       鍵のパスワード (省略時は keystore と同じ)
// いずれも無ければ何もしない (debug keystore のまま)。
const { withAppBuildGradle } = require("expo/config-plugins");

function gradleString(value) {
  return JSON.stringify(String(value));
}

module.exports = function withReleaseSigning(config) {
  const storeFile = process.env.ANDROID_KEYSTORE_PATH;
  const storePassword = process.env.ANDROID_KEYSTORE_PASSWORD;
  const keyAlias = process.env.ANDROID_KEY_ALIAS;
  const keyPassword = process.env.ANDROID_KEY_PASSWORD || storePassword;
  if (!storeFile || !storePassword || !keyAlias) {
    return config;
  }

  return withAppBuildGradle(config, (mod) => {
    let gradle = mod.modResults.contents;
    if (gradle.includes("signingConfigs.release")) {
      return mod;
    }
    const releaseConfig = [
      "        release {",
      `            storeFile file(${gradleString(storeFile)})`,
      `            storePassword ${gradleString(storePassword)}`,
      `            keyAlias ${gradleString(keyAlias)}`,
      `            keyPassword ${gradleString(keyPassword)}`,
      "        }",
    ].join("\n");
    // signingConfigs { debug { ... } } の直後に release を追加する
    gradle = gradle.replace(/(signingConfigs\s*\{\s*debug\s*\{[\s\S]*?\n\s*\}\n)/, `$1${releaseConfig}\n`);
    // buildTypes.release の signingConfig を差し替える
    gradle = gradle.replace(
      /(buildTypes\s*\{[\s\S]*?release\s*\{[\s\S]*?signingConfig\s+)signingConfigs\.debug/,
      "$1signingConfigs.release",
    );
    if (!gradle.includes("signingConfigs.release")) {
      throw new Error("withReleaseSigning: android/app/build.gradle の署名設定を書き換えられませんでした");
    }
    mod.modResults.contents = gradle;
    return mod;
  });
};
