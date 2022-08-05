package fcm

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	fcmv1 "google.golang.org/api/fcm/v1"

	"github.com/tinode/chat/server/drafty"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/push/common"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

const (
	// TTL of a VOIP push notification in seconds.
	voipTimeToLive = 10
	// TTL of a regular push notification in seconds.
	defaultTimeToLive = 3600
)

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
		if pl.ContentType != "" {
			data["mime"] = pl.ContentType
		}

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

// PrepareV1Notifications creates notification payloads ready to be posted
// to push notification server for the provided receipt.
func PrepareV1Notifications(rcpt *push.Receipt, config *configType) ([]*fcmv1.Message, []t.Uid) {
	data, err := payloadToData(&rcpt.Payload)
	if err != nil {
		logs.Warn.Println("fcm push: could not parse payload:", err)
		return nil, nil
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
			return nil, nil
		}
	}
	if count == 0 && rcpt.Channel == "" {
		return nil, nil
	}

	if config == nil {
		// config is nil when called from tnpg adapter; provide a blank one for simplicity.
		config = &configType{}
	}

	var messages []*fcmv1.Message
	var uids []t.Uid
	for uid, devList := range devices {
		topic := rcpt.Payload.Topic
		userData := data
		tcat := t.GetTopicCat(topic)
		if rcpt.To[uid].Delivered > 0 || tcat == t.TopicCatP2P {
			userData = clonePayload(data)
			// Fix topic name for P2P pushes.
			if tcat == t.TopicCatP2P {
				topic, _ = t.P2PNameForUser(uid, topic)
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

				switch d.Platform {
				case "android":
					msg.Android = androidNotificationConfig(rcpt.Payload.What, topic, userData, config)
				case "ios":
					msg.Apns = apnsNotificationConfig(rcpt.Payload.What, topic, userData, rcpt.To[uid].Unread, config)
				case "web":
					if config != nil && config.Webpush != nil && config.Webpush.Enabled {
						msg.Webpush = &fcmv1.WebpushConfig{}
					}
				case "":
					// ignore
				default:
					logs.Warn.Println("fcm: unknown device platform", d.Platform)
				}

				uids = append(uids, uid)
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
		}

		// We don't know the platform of the receiver, must provide payload for all platforms.
		msg.Android = androidNotificationConfig(rcpt.Payload.What, topic, userData, config)
		msg.Apns = apnsNotificationConfig(rcpt.Payload.What, topic, userData, 0, config)
		// TODO: add webpush payload.
		messages = append(messages, &msg)
		// UID is not used in handling Topic pushes, but should keep the same count as messages.
		uids = append(uids, t.ZeroUid)
	}

	return messages, uids
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

func androidNotificationConfig(what, topic string, data map[string]string, config *configType) *fcmv1.AndroidConfig {
	timeToLive := strconv.Itoa(defaultTimeToLive) + "s"
	if config != nil && config.TimeToLive > 0 {
		timeToLive = strconv.Itoa(config.TimeToLive) + "s"
	}

	if what == push.ActRead {
		return &fcmv1.AndroidConfig{
			Priority:     string(common.AndroidPriorityNormal),
			Notification: nil,
			Ttl:          timeToLive,
		}
	}

	_, videoCall := data["webrtc"]
	if videoCall {
		timeToLive = "0s"
	}

	// Sending priority.
	priority := string(common.AndroidPriorityHigh)
	ac := &fcmv1.AndroidConfig{
		Priority: priority,
		Ttl:      timeToLive,
	}

	// When this notification type is included and the app is not in the foreground
	// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
	// See dicussion: https://github.com/firebase/quickstart-js/issues/71
	if config.Android == nil || !config.Android.Enabled {
		return ac
	}

	body := config.Android.GetStringField(what, "Body")
	if body == "$content" {
		body = data["content"]
	}

	// Client-side display priority.
	priority = string(common.AndroidNotificationPriorityHigh)
	if videoCall {
		priority = string(common.AndroidNotificationPriorityMax)
	}

	ac.Notification = &fcmv1.AndroidNotification{
		// Android uses Tag value to group notifications together:
		// show just one notification per topic.
		Tag:                  topic,
		NotificationPriority: priority,
		Visibility:           string(common.AndroidVisibilityPrivate),
		TitleLocKey:          config.Android.GetStringField(what, "TitleLocKey"),
		Title:                config.Android.GetStringField(what, "Title"),
		BodyLocKey:           config.Android.GetStringField(what, "BodyLocKey"),
		Body:                 body,
		Icon:                 config.Android.GetStringField(what, "Icon"),
		Color:                config.Android.GetStringField(what, "Color"),
		ClickAction:          config.Android.GetStringField(what, "ClickAction"),
	}

	return ac
}

func apnsNotificationConfig(what, topic string, data map[string]string, unread int, config *configType) *fcmv1.ApnsConfig {
	callStatus := data["webrtc"]
	expires := time.Now().UTC().Add(time.Duration(defaultTimeToLive) * time.Second)
	if config.TimeToLive > 0 {
		expires = time.Now().UTC().Add(time.Duration(config.TimeToLive) * time.Second)
	}
	bundleId := config.ApnsBundleID
	pushType := common.ApnsPushTypeAlert
	priority := 10
	interruptionLevel := common.InterruptionLevelTimeSensitive
	if callStatus == "started" {
		// Send VOIP push only when a new call is started, otherwise send normal alert.
		interruptionLevel = common.InterruptionLevelCritical
		// FIXME: PushKit notifications do not work with the current FCM adapter.
		// Using normal pushes as a poor-man's replacement for VOIP pushes.
		// Uncomment the following two lines when FCM fixes its problem or when we switch to
		// a different adapter.
		// pushType = common.ApnsPushTypeVoip
		// bundleId += ".voip"
		expires = time.Now().UTC().Add(time.Duration(voipTimeToLive) * time.Second)
	} else if what == push.ActRead {
		priority = 5
		interruptionLevel = common.InterruptionLevelPassive
		pushType = common.ApnsPushTypeBackground
	}

	apsPayload := common.Aps{
		Badge:             unread,
		ContentAvailable:  1,
		MutableContent:    1,
		InterruptionLevel: interruptionLevel,
		ThreadID:          topic,
	}

	if config.Apns != nil && config.Apns.Enabled && what != push.ActRead {
		body := config.Apns.GetStringField(what, "Body")
		if body == "$content" {
			body = data["content"]
		}

		apsPayload.Alert = &common.ApsAlert{
			Action:          config.Apns.GetStringField(what, "Action"),
			ActionLocKey:    config.Apns.GetStringField(what, "ActionLocKey"),
			Body:            body,
			LaunchImage:     config.Apns.GetStringField(what, "LaunchImage"),
			LocKey:          config.Apns.GetStringField(what, "LocKey"),
			Title:           config.Apns.GetStringField(what, "Title"),
			Subtitle:        config.Apns.GetStringField(what, "Subtitle"),
			TitleLocKey:     config.Apns.GetStringField(what, "TitleLocKey"),
			SummaryArg:      config.Apns.GetStringField(what, "SummaryArg"),
			SummaryArgCount: config.Apns.GetIntField(what, "SummaryArgCount"),
		}
	}

	payload, err := json.Marshal(map[string]interface{}{"aps": apsPayload})
	if err != nil {
		return nil
	}
	headers := map[string]string{
		common.HeaderApnsExpiration: strconv.FormatInt(expires.Unix(), 10),
		common.HeaderApnsPriority:   strconv.Itoa(priority),
		common.HeaderApnsTopic:      bundleId,
		common.HeaderApnsCollapseID: topic,
		common.HeaderApnsPushType:   string(pushType),
	}

	ac := &fcmv1.ApnsConfig{
		Headers: headers,
		Payload: payload,
	}

	return ac
}
