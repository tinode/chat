package fcm

import (
	"errors"
	"strconv"
	"time"

	legacy "firebase.google.com/go/messaging"
	fcmv1 "google.golang.org/api/fcm/v1"

	"github.com/tinode/chat/server/drafty"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// AndroidConfig is the configuration of AndroidNotification payload.
type AndroidConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// Common defaults for all push types.
	androidPayload
	// Configs for specific push types.
	Msg androidPayload `json:"msg,omitempty"`
	Sub androidPayload `json:"sub,omitempty"`
}

func (ac *AndroidConfig) getTitleLocKey(what string) string {
	var title string
	if what == push.ActMsg {
		title = ac.Msg.TitleLocKey
	} else if what == push.ActSub {
		title = ac.Sub.TitleLocKey
	}
	if title == "" {
		title = ac.androidPayload.TitleLocKey
	}
	return title
}

func (ac *AndroidConfig) getTitle(what string) string {
	var title string
	if what == push.ActMsg {
		title = ac.Msg.Title
	} else if what == push.ActSub {
		title = ac.Sub.Title
	}
	if title == "" {
		title = ac.androidPayload.Title
	}
	return title
}

func (ac *AndroidConfig) getBodyLocKey(what string) string {
	var body string
	if what == push.ActMsg {
		body = ac.Msg.BodyLocKey
	} else if what == push.ActSub {
		body = ac.Sub.BodyLocKey
	}
	if body == "" {
		body = ac.androidPayload.BodyLocKey
	}
	return body
}

func (ac *AndroidConfig) getBody(what string) string {
	var body string
	if what == push.ActMsg {
		body = ac.Msg.Body
	} else if what == push.ActSub {
		body = ac.Sub.Body
	}
	if body == "" {
		body = ac.androidPayload.Body
	}
	return body
}

func (ac *AndroidConfig) getIcon(what string) string {
	var icon string
	if what == push.ActMsg {
		icon = ac.Msg.Icon
	} else if what == push.ActSub {
		icon = ac.Sub.Icon
	}
	if icon == "" {
		icon = ac.androidPayload.Icon
	}
	return icon
}

func (ac *AndroidConfig) getColor(what string) string {
	var color string
	if what == push.ActMsg {
		color = ac.Msg.Color
	} else if what == push.ActSub {
		color = ac.Sub.Color
	}
	if color == "" {
		color = ac.androidPayload.Color
	}
	return color
}

func (ac *AndroidConfig) getClickAction(what string) string {
	var clickAction string
	if what == push.ActMsg {
		clickAction = ac.Msg.ClickAction
	} else if what == push.ActSub {
		clickAction = ac.Sub.ClickAction
	}
	if clickAction == "" {
		clickAction = ac.androidPayload.ClickAction
	}
	return clickAction
}

// Payload to be sent for a specific notification type.
type androidPayload struct {
	TitleLocKey string `json:"title_loc_key,omitempty"`
	Title       string `json:"title,omitempty"`
	BodyLocKey  string `json:"body_loc_key,omitempty"`
	Body        string `json:"body,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	ClickAction string `json:"click_action,omitempty"`
}

// InterruptionLevel defines the value for the payload aps interruption-level
type InterruptionLevelType string

const (
	// InterruptionLevelPassive is used to indicate that notification be delivered in a passive manner.
	InterruptionLevelPassive InterruptionLevelType = "passive"

	// InterruptionLevelActive is used to indicate the importance and delivery timing of a notification.
	InterruptionLevelActive InterruptionLevelType = "active"

	// InterruptionLevelTimeSensitive is used to indicate the importance and delivery timing of a notification.
	InterruptionLevelTimeSensitive InterruptionLevelType = "time-sensitive"

	// InterruptionLevelCritical is used to indicate the importance and delivery timing of a notification.
	// This interruption level requires an approved entitlement from Apple.
	// See: https://developer.apple.com/documentation/usernotifications/unnotificationinterruptionlevel/
	InterruptionLevelCritical InterruptionLevelType = "critical"
)

// aps is the APNS payload.
type aps struct {
	Alert             apsAlert              `json:"alert,omitempty"`
	Badge             int                   `json:"badge,omitempty"`
	Category          string                `json:"category,omitempty"`
	ContentAvailable  int                   `json:"content-available,omitempty"`
	InterruptionLevel InterruptionLevelType `json:"interruption-level,omitempty"`
	MutableContent    int                   `json:"mutable-content,omitempty"`
	RelevanceScore    interface{}           `json:"relevance-score,omitempty"`
	Sound             interface{}           `json:"sound,omitempty"`
	ThreadID          string                `json:"thread-id,omitempty"`
	URLArgs           []string              `json:"url-args,omitempty"`
}

// apsAlert is the content of the aps.Alert field.
type apsAlert struct {
	Action          string   `json:"action,omitempty"`
	ActionLocKey    string   `json:"action-loc-key,omitempty"`
	Body            string   `json:"body,omitempty"`
	LaunchImage     string   `json:"launch-image,omitempty"`
	LocArgs         []string `json:"loc-args,omitempty"`
	LocKey          string   `json:"loc-key,omitempty"`
	Title           string   `json:"title,omitempty"`
	Subtitle        string   `json:"subtitle,omitempty"`
	TitleLocArgs    []string `json:"title-loc-args,omitempty"`
	TitleLocKey     string   `json:"title-loc-key,omitempty"`
	SummaryArg      string   `json:"summary-arg,omitempty"`
	SummaryArgCount int      `json:"summary-arg-count,omitempty"`
}

// MessageData adds user ID and device token to push message. This is needed for error handling.
type MessageData struct {
	Uid t.Uid
	// FCM device token.
	DeviceId string
	Message  *legacy.Message
}

func payloadToData(pl *push.Payload) (map[string]string, error) {
	if pl == nil {
		return nil, errors.New("empty push payload")
	}
	data := make(map[string]string)
	var err error
	data["what"] = pl.What
	if pl.Silent {
		data["silent"] = "true"
	}
	data["topic"] = pl.Topic
	data["ts"] = pl.Timestamp.Format(time.RFC3339Nano)
	// Must use "xfrom" because "from" is a reserved word. Google did not bother to document it anywhere.
	data["xfrom"] = pl.From
	if pl.What == push.ActMsg {
		data["seq"] = strconv.Itoa(pl.SeqId)
		data["mime"] = pl.ContentType

		// Convert Drafty content to plain text (clients 0.16 and below).
		data["content"], err = drafty.PlainText(pl.Content)
		if err != nil {
			return nil, err
		}
		// Trim long strings to 128 runes.
		// Check byte length first and don't waste time converting short strings.
		if len(data["content"]) > push.MaxPayloadLength {
			runes := []rune(data["content"])
			if len(runes) > push.MaxPayloadLength {
				data["content"] = string(runes[:push.MaxPayloadLength]) + "…"
			}
		}

		// Rich content for clients version 0.17 and above.
		data["rc"], err = drafty.Preview(pl.Content, push.MaxPayloadLength)

		if pl.Webrtc != "" {
			data["webrtc"] = pl.Webrtc
			// Video call push notifications are silent.
			data["silent"] = "true"
		}
		if pl.Replace != "" {
			data["replace"] = pl.Replace
		}
		if err != nil {
			return nil, err
		}
	} else if pl.What == push.ActSub {
		data["modeWant"] = pl.ModeWant.String()
		data["modeGiven"] = pl.ModeGiven.String()
	} else if pl.What == push.ActRead {
		data["seq"] = strconv.Itoa(pl.SeqId)
		data["silent"] = "true"
	} else {
		return nil, errors.New("unknown push type")
	}
	return data, nil
}

func clonePayload(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, val := range src {
		dst[key] = val
	}
	return dst
}

// PrepareLegacyNotifications creates notification payloads ready to be posted
// to legacy push notification server for the provided receipt.
func PrepareLegacyNotifications(rcpt *push.Receipt, config *AndroidConfig) []MessageData {
	data, err := payloadToData(&rcpt.Payload)
	if err != nil {
		logs.Warn.Println("fcm push: could not parse payload;", err)
		return nil
	}

	// Device IDs to send pushes to.
	var devices map[t.Uid][]t.DeviceDef
	// Count of device IDs to push to.
	var count int
	// Devices which were online in the topic when the message was sent.
	skipDevices := make(map[string]struct{})
	if len(rcpt.To) > 0 {
		// List of UIDs for querying the database

		uids := make([]t.Uid, len(rcpt.To))
		i := 0
		for uid, to := range rcpt.To {
			uids[i] = uid
			i++
			// Some devices were online and received the message. Skip them.
			for _, deviceID := range to.Devices {
				skipDevices[deviceID] = struct{}{}
			}
		}
		devices, count, err = store.Devices.GetAll(uids...)
		if err != nil {
			logs.Warn.Println("fcm push: db error", err)
			return nil
		}
	}
	if count == 0 && rcpt.Channel == "" {
		return nil
	}

	var title, body, priority string
	var androidNotification func(msg *legacy.Message, priority string)
	if rcpt.Payload.What == push.ActRead {
		priority = "normal"
		androidNotification = func(msg *legacy.Message, priority string) {
			msg.Android = &legacy.AndroidConfig{
				Priority:     priority,
				Notification: nil,
			}
		}
	} else {
		priority = "high"
		var titlelc, bodylc, icon, color, clickAction string
		if config != nil && config.Enabled {
			titlelc = config.getTitleLocKey(rcpt.Payload.What)
			title = config.getTitle(rcpt.Payload.What)
			bodylc = config.getBodyLocKey(rcpt.Payload.What)
			body = config.getBody(rcpt.Payload.What)
			if body == "$content" {
				body = data["content"]
			}
			icon = config.getIcon(rcpt.Payload.What)
			color = config.getColor(rcpt.Payload.What)
			clickAction = config.getClickAction(rcpt.Payload.What)
		}

		androidNotification = func(msg *legacy.Message, priority string) {
			msg.Android = &legacy.AndroidConfig{
				Priority: priority,
			}
			// When this notification type is included and the app is not in the foreground
			// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
			// See dicussion: https://github.com/firebase/quickstart-js/issues/71
			if config != nil && config.Enabled {
				msg.Android.Notification = &legacy.AndroidNotification{
					// Android uses Tag value to group notifications together:
					// show just one notification per topic.
					Tag:         rcpt.Payload.Topic,
					Priority:    legacy.PriorityHigh,
					Visibility:  legacy.VisibilityPrivate,
					TitleLocKey: titlelc,
					Title:       title,
					BodyLocKey:  bodylc,
					Body:        body,
					Icon:        icon,
					Color:       color,
					ClickAction: clickAction,
				}
			}
		}
	}

	// TODO(aforge): introduce iOS push configuration (similar to Android).
	// FIXME(gene): need to configure silent "read" push.
	_, isVideoCall := data["webrtc"]
	titleIOS := "New message"
	bodyIOS := data["content"]
	apnsNotification := func(msg *legacy.Message, unread int) {
		msg.APNS = &legacy.APNSConfig{
			Payload: &legacy.APNSPayload{
				Aps: &legacy.Aps{
					ContentAvailable: true,
					MutableContent:   true,
				},
			},
		}
		if isVideoCall {
			// Sound, alert and badge are not available for video call messages.
			return
		}
		msg.APNS.Payload.Aps.Sound = "default"
		// Need to duplicate these in APNS.Payload.Aps.Alert so
		// iOS may call NotificationServiceExtension (if present).
		msg.APNS.Payload.Aps.Alert = &legacy.ApsAlert{
			Title: titleIOS,
			Body:  bodyIOS,
		}
		if unread >= 0 {
			// iOS uses Badge to show the total unread message count.
			msg.APNS.Payload.Aps.Badge = &unread
		}
	}

	var messages []MessageData
	for uid, devList := range devices {
		userData := data
		tcat := t.GetTopicCat(data["topic"])
		if rcpt.To[uid].Delivered > 0 || tcat == t.TopicCatP2P {
			userData = clonePayload(data)
			// Fix topic name for P2P pushes.
			if tcat == t.TopicCatP2P {
				topic, _ := t.P2PNameForUser(uid, data["topic"])
				userData["topic"] = topic
			}
			// Silence the push for user who have received the data interactively.
			if rcpt.To[uid].Delivered > 0 {
				userData["silent"] = "true"
			}
		}
		for i := range devList {
			d := &devList[i]
			if _, ok := skipDevices[d.DeviceId]; !ok && d.DeviceId != "" {
				msg := legacy.Message{
					Token: d.DeviceId,
					Data:  userData,
				}

				if title != "" || body != "" {
					msg.Notification = &legacy.Notification{
						Title: title,
						Body:  body,
					}
				}

				if d.Platform == "android" {
					androidNotification(&msg, priority)
				} else if d.Platform == "ios" {
					apnsNotification(&msg, rcpt.To[uid].Unread)
				}
				messages = append(messages, MessageData{Uid: uid, DeviceId: d.DeviceId, Message: &msg})
			}
		}
	}

	if rcpt.Channel != "" {
		topic := rcpt.Channel
		userData := clonePayload(data)
		userData["topic"] = topic
		// Channel receiver should not know the ID of the message sender.
		delete(userData, "xfrom")
		msg := legacy.Message{
			Topic: topic,
			Data:  userData,
			Notification: &legacy.Notification{
				Title: title,
				Body:  body,
			},
		}

		// We don't know the platform of the receiver, must provide payload for all platforms.
		androidNotification(&msg, "normal")
		apnsNotification(&msg, -1)
		messages = append(messages, MessageData{Message: &msg})
	}

	return messages
}

// PrepareV1Notifications creates notification payloads ready to be posted
// to push notification server for the provided receipt.
func PrepareV1Notifications(rcpt *push.Receipt, config *configType) []*fcmv1.Message {
	data, err := payloadToData(&rcpt.Payload)
	if err != nil {
		logs.Warn.Println("fcm push: could not parse payload;", err)
		return nil
	}

	// Device IDs to send pushes to.
	var devices map[t.Uid][]t.DeviceDef
	// Count of device IDs to push to.
	var count int
	// Devices which were online in the topic when the message was sent.
	skipDevices := make(map[string]struct{})
	if len(rcpt.To) > 0 {
		// List of UIDs for querying the database

		uids := make([]t.Uid, len(rcpt.To))
		i := 0
		for uid, to := range rcpt.To {
			uids[i] = uid
			i++
			// Some devices were online and received the message. Skip them.
			for _, deviceID := range to.Devices {
				skipDevices[deviceID] = struct{}{}
			}
		}
		devices, count, err = store.Devices.GetAll(uids...)
		if err != nil {
			logs.Warn.Println("fcm push: db error", err)
			return nil
		}
	}
	if count == 0 && rcpt.Channel == "" {
		return nil
	}

	var title, body, priority string

	// TODO(aforge): introduce iOS push configuration (similar to Android).
	// FIXME(gene): need to configure silent "read" push.
	_, isVideoCall := data["webrtc"]
	titleIOS := "New message"
	bodyIOS := data["content"]
	apnsNotification := func(msg *legacy.Message, unread int) {
		msg.APNS = &legacy.APNSConfig{
			Payload: &legacy.APNSPayload{
				Aps: &legacy.Aps{
					ContentAvailable: true,
					MutableContent:   true,
				},
			},
		}
		if isVideoCall {
			// Sound, alert and badge are not available for video call messages.
			return
		}
		msg.APNS.Payload.Aps.Sound = "default"
		// Need to duplicate these in APNS.Payload.Aps.Alert so
		// iOS may call NotificationServiceExtension (if present).
		msg.APNS.Payload.Aps.Alert = &legacy.ApsAlert{
			Title: titleIOS,
			Body:  bodyIOS,
		}
		if unread >= 0 {
			// iOS uses Badge to show the total unread message count.
			msg.APNS.Payload.Aps.Badge = &unread
		}
	}

	var messages []*fcmv1.Message
	for uid, devList := range devices {
		userData := data
		tcat := t.GetTopicCat(data["topic"])
		if rcpt.To[uid].Delivered > 0 || tcat == t.TopicCatP2P {
			userData = clonePayload(data)
			// Fix topic name for P2P pushes.
			if tcat == t.TopicCatP2P {
				topic, _ := t.P2PNameForUser(uid, data["topic"])
				userData["topic"] = topic
			}
			// Silence the push for user who have received the data interactively.
			if rcpt.To[uid].Delivered > 0 {
				userData["silent"] = "true"
			}
		}

		for i := range devList {
			d := &devList[i]
			if _, ok := skipDevices[d.DeviceId]; !ok && d.DeviceId != "" {
				msg := fcmv1.Message{
					Token: d.DeviceId,
					Data:  userData,
				}

				if title != "" || body != "" {
					msg.Notification = &fcmv1.Notification{
						Title: title,
						Body:  body,
					}
				}

				switch d.Platform {
				case "android":
					if config.Android.Enabled {
						msg.Android = &fcmv1.AndroidConfig{}
					}
				case "ios":
					if config.Apns.Enabled {
						msg.Apns = &fcmv1.ApnsConfig{}
					}
				case "web":
					if config.Webpush.Enabled {
						msg.Webpush = &fcmv1.WebpushConfig{}
					}
				case "":
					// ignore
				default:
					logs.Warn.Println("fcm: unknown device platform", d.Platform)
				}
				messages = append(messages, &msg)
			}
		}
	}

	if rcpt.Channel != "" {
		topic := rcpt.Channel
		userData := clonePayload(data)
		userData["topic"] = topic
		// Channel receiver should not know the ID of the message sender.
		delete(userData, "xfrom")
		msg := fcmv1.Message{
			Topic: topic,
			Data:  userData,
			Notification: &fcmv1.Notification{
				Title: title,
				Body:  body,
			},
		}

		// We don't know the platform of the receiver, must provide payload for all platforms.
		androidNotification(&msg, "normal")
		apnsNotification(&msg, -1)
		messages = append(messages, &msg)
	}

	return messages
}

// DevicesForUser loads device IDs of the given user.
func DevicesForUser(uid t.Uid) []string {
	ddef, count, err := store.Devices.GetAll(uid)
	if err != nil {
		logs.Warn.Println("fcm devices for user: db error", err)
		return nil
	}

	if count == 0 {
		return nil
	}

	devices := make([]string, count)
	for i, dd := range ddef[uid] {
		devices[i] = dd.DeviceId
	}
	return devices
}

// ChannelsForUser loads user's channel subscriptions with P permission.
func ChannelsForUser(uid t.Uid) []string {
	channels, err := store.Users.GetChannels(uid)
	if err != nil {
		logs.Warn.Println("fcm channels for user: db error", err)
		return nil
	}
	return channels
}

// Priority "NORMAL" or "HIGH"
func androidNotificationConfig(what string, config *AndroidConfig) *fcmv1.AndroidConfig {
	if what == push.ActRead {
		return &fcmv1.AndroidConfig{
			Priority:     "NORMAL",
			Notification: nil,
		}
	}

	priority = "HIGH"
	var titlelc, bodylc, icon, color, clickAction string
	if config != nil && config.Enabled {
		titlelc = config.getTitleLocKey(what)
		title = config.getTitle(what)
		bodylc = config.getBodyLocKey(what)
		body = config.getBody(what)
		if body == "$content" {
			body = data["content"]
		}
		icon = config.getIcon(what)
		color = config.getColor(what)
		clickAction = config.getClickAction(what)
	}

	androidNotification = func(msg *legacy.Message, priority string) {
		msg.Android = &legacy.AndroidConfig{
			Priority: priority,
		}
		// When this notification type is included and the app is not in the foreground
		// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
		// See dicussion: https://github.com/firebase/quickstart-js/issues/71
		if config != nil && config.Enabled {
			msg.Android.Notification = &legacy.AndroidNotification{
				// Android uses Tag value to group notifications together:
				// show just one notification per topic.
				Tag:         rcpt.Payload.Topic,
				Priority:    legacy.PriorityHigh,
				Visibility:  legacy.VisibilityPrivate,
				TitleLocKey: titlelc,
				Title:       title,
				BodyLocKey:  bodylc,
				Body:        body,
				Icon:        icon,
				Color:       color,
				ClickAction: clickAction,
			}
		}
	}
}
