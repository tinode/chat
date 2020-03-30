package fcm

import (
	"errors"
	"log"
	"strconv"
	"time"

	fcm "firebase.google.com/go/messaging"

	"github.com/tinode/chat/server/drafty"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Configuration of AndroidNotification payload.
type AndroidConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// Common defauls for all push types.
	androidPayload
	// Configs for specific push types.
	Msg androidPayload `json:"msg,omitempty"`
	Sub androidPayload `json:"msg,omitempty"`
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

func (ac *AndroidConfig) getIconColor(what string) string {
	var color string
	if what == push.ActMsg {
		color = ac.Msg.IconColor
	} else if what == push.ActSub {
		color = ac.Sub.IconColor
	}
	if color == "" {
		color = ac.androidPayload.IconColor
	}
	return color
}

// Payload to be sent for a specific notification type.
type androidPayload struct {
	TitleLocKey string `json:"title_loc_key,omitempty"`
	Title       string `json:"title,omitempty"`
	BodyLocKey  string `json:"body_loc_key,omitempty"`
	Body        string `json:"body,omitempty"`
	Icon        string `json:"icon,omitempty"`
	IconColor   string `json:"icon_color,omitempty"`
	ClickAction string `json:"click_action,omitempty"`
}

type messageData struct {
	Uid      t.Uid
	DeviceId string
	Message  *fcm.Message
}

func payloadToData(pl *push.Payload) (map[string]string, error) {
	if pl == nil {
		return nil, nil
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
		data["content"], err = drafty.ToPlainText(pl.Content)
		if err != nil {
			return nil, err
		}

		// Trim long strings to 80 runes.
		// Check byte length first and don't waste time converting short strings.
		if len(data["content"]) > maxMessageLength {
			runes := []rune(data["content"])
			if len(runes) > maxMessageLength {
				data["content"] = string(runes[:maxMessageLength]) + "…"
			}
		}
	} else if pl.What == push.ActSub {
		data["modeWant"] = pl.ModeWant.String()
		data["modeGiven"] = pl.ModeGiven.String()
	} else {
		return nil, errors.New("unknown push type")
	}
	return data, nil
}

// PrepareNotifications creates notification payloads ready to be posted
// to push notification server for the provided receipt.
func PrepareNotifications(rcpt *push.Receipt, config *AndroidConfig) []messageData {
	data, _ := payloadToData(&rcpt.Payload)
	if data == nil {
		log.Println("fcm push: could not parse payload")
		return nil
	}

	// List of UIDs for querying the database
	uids := make([]t.Uid, len(rcpt.To))
	skipDevices := make(map[string]bool)
	i := 0
	for uid, to := range rcpt.To {
		uids[i] = uid
		i++

		// Some devices were online and received the message. Skip them.
		for _, deviceID := range to.Devices {
			skipDevices[deviceID] = true
		}
	}

	devices, count, err := store.Devices.GetAll(uids...)
	if err != nil {
		log.Println("fcm push: db error", err)
		return nil
	}
	if count == 0 {
		return nil
	}

	var titlelc, title, bodylc, body, icon, color string
	if config != nil && config.Enabled {
		titlelc = config.getTitleLocKey(rcpt.Payload.What)
		title = config.getTitle(rcpt.Payload.What)
		bodylc = config.getBodyLocKey(rcpt.Payload.What)
		body = config.getBody(rcpt.Payload.What)
		if body == "$content" {
			body = data["content"]
		}
		icon = config.getIcon(rcpt.Payload.What)
		color = config.getIconColor(rcpt.Payload.What)
	}

	var messages []messageData
	for uid, devList := range devices {
		for i := range devList {
			d := &devList[i]
			if _, ok := skipDevices[d.DeviceId]; !ok && d.DeviceId != "" {
				msg := fcm.Message{
					Token: d.DeviceId,
					Data:  data,
				}

				if d.Platform == "android" {
					msg.Android = &fcm.AndroidConfig{
						Priority: "high",
					}
					if config != nil && config.Enabled {
						// When this notification type is included and the app is not in the foreground
						// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
						// See dicussion: https://github.com/firebase/quickstart-js/issues/71
						msg.Android.Notification = &fcm.AndroidNotification{
							// Android uses Tag value to group notifications together:
							// show just one notification per topic.
							Tag:         rcpt.Payload.Topic,
							TitleLocKey: titlelc,
							Title:       title,
							BodyLocKey:  bodylc,
							Body:        body,
							Icon:        icon,
							Color:       color,
						}
					}
				} else if d.Platform == "ios" {
					// iOS uses Badge to show the total unread message count.
					badge := rcpt.To[uid].Unread
					// Need to duplicate these in APNS.Payload.Aps.Alert so
					// iOS may call NotificationServiceExtension (if present).
					title := "New message"
					body := data["content"]
					msg.APNS = &fcm.APNSConfig{
						Payload: &fcm.APNSPayload{
							Aps: &fcm.Aps{
								Badge:            &badge,
								ContentAvailable: true,
								MutableContent:   true,
								Sound:            "default",
								Alert: &fcm.ApsAlert{
									Title: title,
									Body:  body,
								},
							},
						},
					}
					msg.Notification = &fcm.Notification{
						Title: title,
						Body:  body,
					}
				}
				messages = append(messages, messageData{Uid: uid, DeviceId: d.DeviceId, Message: &msg})
			}
		}
	}
	return messages
}
