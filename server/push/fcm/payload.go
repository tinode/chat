package fcm

import (
	"errors"
	"strconv"
	"time"

	fcm "firebase.google.com/go/messaging"

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

// MessageData adds user ID and device token to push message. This is needed for error handling.
type MessageData struct {
	Uid t.Uid
	// FCM device token.
	DeviceId string
	Message  *fcm.Message
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
		if err != nil {
			return nil, err
		}
	} else if pl.What == push.ActSub {
		data["modeWant"] = pl.ModeWant.String()
		data["modeGiven"] = pl.ModeGiven.String()
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

// PrepareNotifications creates notification payloads ready to be posted
// to push notification server for the provided receipt.
func PrepareNotifications(rcpt *push.Receipt, config *AndroidConfig) []MessageData {
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

	var titlelc, title, bodylc, body, icon, color, clickAction string
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

	androidNotification := func(msg *fcm.Message) {
		// When this notification type is included and the app is not in the foreground
		// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
		// See dicussion: https://github.com/firebase/quickstart-js/issues/71
		if config != nil && config.Enabled {
			msg.Android.Notification = &fcm.AndroidNotification{
				// Android uses Tag value to group notifications together:
				// show just one notification per topic.
				Tag:         rcpt.Payload.Topic,
				Priority:    fcm.PriorityHigh,
				Visibility:  fcm.VisibilityPrivate,
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

	// TODO(aforge): introduce iOS push configuration (similar to Android).
	titleIOS := "New message"
	bodyIOS := data["content"]
	apnsNotification := func(msg *fcm.Message) {
		msg.APNS = &fcm.APNSConfig{
			Payload: &fcm.APNSPayload{
				Aps: &fcm.Aps{
					ContentAvailable: true,
					MutableContent:   true,
					Sound:            "default",
					// Need to duplicate these in APNS.Payload.Aps.Alert so
					// iOS may call NotificationServiceExtension (if present).
					Alert: &fcm.ApsAlert{
						Title: titleIOS,
						Body:  bodyIOS,
					},
				},
			},
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
				msg := fcm.Message{
					Token: d.DeviceId,
					Data:  userData,
					Notification: &fcm.Notification{
						Title: title,
						Body:  body,
					},
				}

				if d.Platform == "android" {
					msg.Android = &fcm.AndroidConfig{
						Priority: "high",
					}
					androidNotification(&msg)
				} else if d.Platform == "ios" {
					apnsNotification(&msg)
					// iOS uses Badge to show the total unread message count.
					badge := rcpt.To[uid].Unread
					msg.APNS.Payload.Aps.Badge = &badge
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
		msg := fcm.Message{
			Topic: topic,
			Data:  userData,
			Notification: &fcm.Notification{
				Title: title,
				Body:  body,
			},
		}

		msg.Android = &fcm.AndroidConfig{
			Priority: "normal",
		}
		androidNotification(&msg)
		apnsNotification(&msg)
		messages = append(messages, MessageData{Message: &msg})
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
