package push

import (
	"context"
	"fmt"
)

// NativeSender はネイティブのプッシュ通知 (Android は FCM、iOS は APNs) を
// Message.Platform で振り分けて送る Sender。どちらかの Sender が nil の
// 場合、そのプラットフォーム向けのメッセージはエラーになる
// (FCMSender / APNSSender を参照)。
type NativeSender struct {
	// Android は Android 向けの Sender (通常は *FCMSender)。nil 可。
	Android Sender
	// IOS は iOS 向けの Sender (通常は *APNSSender)。nil 可。
	IOS Sender
}

// Send implements Sender.
func (s NativeSender) Send(ctx context.Context, messages []Message) ([]Result, error) {
	results := make([]Result, len(messages))

	var androidIdx, iosIdx []int
	var androidMsgs, iosMsgs []Message
	for i, m := range messages {
		if m.DeviceToken == "" {
			results[i] = Result{Error: "device has no native token"}
			continue
		}
		switch m.Platform {
		case "android":
			androidIdx = append(androidIdx, i)
			androidMsgs = append(androidMsgs, m)
		case "ios":
			iosIdx = append(iosIdx, i)
			iosMsgs = append(iosMsgs, m)
		default:
			results[i] = Result{Error: fmt.Sprintf("native: unsupported platform %q", m.Platform)}
		}
	}

	if err := sendGroup(ctx, s.Android, "no fcm sender configured", androidIdx, androidMsgs, results); err != nil {
		return nil, err
	}
	if err := sendGroup(ctx, s.IOS, "no apns sender configured", iosIdx, iosMsgs, results); err != nil {
		return nil, err
	}
	return results, nil
}

// sendGroup sends msgs (all belonging to the same platform) through sender,
// writing results back at their original indices (idxs). If sender is nil,
// every message in the group gets missingErr instead of being sent.
func sendGroup(ctx context.Context, sender Sender, missingErr string, idxs []int, msgs []Message, results []Result) error {
	if len(idxs) == 0 {
		return nil
	}
	if sender == nil {
		for _, i := range idxs {
			results[i] = Result{Error: missingErr}
		}
		return nil
	}
	res, err := sender.Send(ctx, msgs)
	if err != nil {
		return err
	}
	if len(res) != len(msgs) {
		return fmt.Errorf("native: result count mismatch: got %d want %d", len(res), len(msgs))
	}
	for j, i := range idxs {
		results[i] = res[j]
	}
	return nil
}
