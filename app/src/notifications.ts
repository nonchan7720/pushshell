import * as Application from "expo-application";
import * as Device from "expo-device";
import * as Localization from "expo-localization";
import * as Notifications from "expo-notifications";
import { Platform } from "react-native";

import type { DeviceRegistration } from "./api/client";
import { config } from "./config";
import type { PushPermissionStatus } from "./bridge";

/**
 * src/notifications.ts
 *
 * 通知権限の要求、Expo Push Token / device push token の取得、
 * バックエンドに送る DeviceRegistration の組み立てを行う。
 */

const ANDROID_CHANNEL_ID = "default";

/** フォアグラウンド受信時の表示挙動。バナー表示・音を鳴らし、バッジは更新する。 */
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: true,
  }),
});

/** 現在の通知権限状態を取得する (要求はしない)。`ready` イベント等の表示用。 */
export async function getCurrentPushPermission(): Promise<PushPermissionStatus> {
  const current = await Notifications.getPermissionsAsync();
  if (current.granted) return "granted";
  return current.status === Notifications.PermissionStatus.UNDETERMINED
    ? "undetermined"
    : "denied";
}

/**
 * 通知権限を確認し、未確定なら要求する。実機以外 (シミュレーター/エミュレーター) では
 * push token が取得できないため、その旨を "denied" 扱いで返す。
 */
export async function ensurePushPermission(): Promise<PushPermissionStatus> {
  if (!Device.isDevice) {
    console.warn(
      "[notifications] 実機ではないため、プッシュ通知の権限確認をスキップします (シミュレーター/エミュレーター)。",
    );
    return "denied";
  }

  if (Platform.OS === "android") {
    // Android は通知チャンネルを作ってから権限を要求する必要がある。
    await Notifications.setNotificationChannelAsync(ANDROID_CHANNEL_ID, {
      name: "Default",
      importance: Notifications.AndroidImportance.HIGH,
    });
  }

  const current = await Notifications.getPermissionsAsync();
  if (current.granted) return "granted";
  if (current.status === Notifications.PermissionStatus.DENIED && !current.canAskAgain) {
    return "denied";
  }

  const requested = await Notifications.requestPermissionsAsync();
  if (requested.granted) return "granted";
  return requested.status === Notifications.PermissionStatus.UNDETERMINED
    ? "undetermined"
    : "denied";
}

export interface PushTokens {
  expoPushToken?: string;
  devicePushToken?: string;
}

/**
 * Expo Push Token (バックエンドが Expo Push API に投げる用) と、
 * ネイティブの device push token (参考情報。FCM/APNs を直接叩く場合用) を取得する。
 * 権限が無い/取得に失敗した場合は該当フィールドを undefined にする。
 */
export async function getPushTokens(): Promise<PushTokens> {
  const projectId = config.easProjectId;
  if (!projectId) {
    // Expo Push Token の取得には EAS の projectId が必須。
    throw new Error(
      "Expo Push Token を取得するには EAS_PROJECT_ID (app.config.ts の extra.eas.projectId) が必要です。" +
        " `eas init` 等でプロジェクトを作成し、EAS_PROJECT_ID を設定してください。",
    );
  }

  let expoPushToken: string | undefined;
  try {
    const result = await Notifications.getExpoPushTokenAsync({ projectId });
    expoPushToken = result.data;
  } catch (error) {
    console.warn("[notifications] getExpoPushTokenAsync に失敗しました:", error);
  }

  let devicePushToken: string | undefined;
  try {
    const result = await Notifications.getDevicePushTokenAsync();
    devicePushToken = typeof result.data === "string" ? result.data : JSON.stringify(result.data);
  } catch (error) {
    console.warn("[notifications] getDevicePushTokenAsync に失敗しました:", error);
  }

  return { expoPushToken, devicePushToken };
}

/**
 * バックエンドの `POST /v1/devices` に送る DeviceRegistration を組み立てる。
 * `loginId` と push token 以外は端末情報から自動で埋める。
 */
export function buildDeviceRegistration(params: {
  loginId: string;
  installationId: string;
  expoPushToken: string;
  devicePushToken?: string;
}): DeviceRegistration {
  const platform: DeviceRegistration["platform"] = Platform.OS === "ios" ? "ios" : "android";

  return {
    loginId: params.loginId,
    installationId: params.installationId,
    platform,
    pushToken: params.expoPushToken,
    deviceToken: params.devicePushToken,
    appId: Application.applicationId ?? undefined,
    appVersion: Application.nativeApplicationVersion ?? undefined,
    buildNumber: Application.nativeBuildVersion ?? undefined,
    osVersion: Device.osVersion ?? undefined,
    deviceModel: Device.modelName ?? undefined,
    locale: getLocale(),
  };
}

function getLocale(): string | undefined {
  const locales = Localization.getLocales();
  return locales[0]?.languageTag;
}
