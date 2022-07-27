package fcm

import (
	"encoding/json"
	"errors"
	"reflect"
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

// Payload to be sent for a specific notification type.
type commonPayload struct {
	// Common for APNS and Android
	Body         string   `json:"body,omitempty"`
	Title        string   `json:"title,omitempty"`
	TitleLocKey  string   `json:"title_loc_key,omitempty"`
	TitleLocArgs []string `json:"title_loc_args,omitempty"`

	// Android
	BodyLocKey  string   `json:"body_loc_key,omitempty"`
	BodyLocArgs []string `json:"body_loc_args,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Color       string   `json:"color,omitempty"`
	ClickAction string   `json:"click_action,omitempty"`
	Sound       string   `json:"sound,omitempty"`
	Image       string   `json:"image,omitempty"`

	// APNS
	Action          string   `json:"action,omitempty"`
	ActionLocKey    string   `json:"action_loc_key,omitempty"`
	LaunchImage     string   `json:"launch_image,omitempty"`
	LocArgs         []string `json:"loc_args,omitempty"`
	LocKey          string   `json:"loc_key,omitempty"`
	Subtitle        string   `json:"subtitle,omitempty"`
	SummaryArg      string   `json:"summary_arg,omitempty"`
	SummaryArgCount int      `json:"summary_arg_count,omitempty"`
}

// CommonConfig is the configuration of AndroidNotification payload.
type CommonConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// Common defaults for all push types.
	commonPayload
	// Configs for specific push types.
	Msg commonPayload `json:"msg,omitempty"`
	Sub commonPayload `json:"sub,omitempty"`
}

func (cp commonPayload) getStringAttr(field string) string {
	val := reflect.ValueOf(cp).FieldByName(field)
	if !val.IsValid() {
		return ""
	}
	if val.Kind() == reflect.String {
		return val.String()
	}
	return ""
}

func (cp commonPayload) getIntAttr(field string) int {
	val := reflect.ValueOf(cp).FieldByName(field)
	if !val.IsValid() {
		return 0
	}
	if val.Kind() == reflect.Int {
		return int(val.Int())
	}
	return 0
}

func (cc *CommonConfig) getStringField(what, field string) string {
	var val string
	if what == push.ActMsg {
		val = cc.Msg.getStringAttr(field)
	} else if what == push.ActSub {
		val = cc.Sub.getStringAttr(field)
	}
	if val == "" {
		val = cc.commonPayload.getStringAttr(field)
	}
	return val
}

func (cc *CommonConfig) getIntField(what, field string) int {
	var val int
	if what == push.ActMsg {
		val = cc.Msg.getIntAttr(field)
	} else if what == push.ActSub {
		val = cc.Sub.getIntAttr(field)
	}
	if val == 0 {
		val = cc.commonPayload.getIntAttr(field)
	}
	return val
}

// TTL of a VOIP push notification.
const voipTimeToLive = 10

// InterruptionLevel defines the values for the APNS payload.aps.InterruptionLevel.
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

const (
	// A canonical UUID that identifies the notification. If there is an error sending the notification,
	// APNs uses this value to identify the notification to your server.
	// The canonical form is 32 lowercase hexadecimal digits, displayed in five groups separated by hyphens
	// in the form 8-4-4-4-12. An example UUID is as follows: 123e4567-e89b-12d3-a456-42665544000
	// If you omit this header, a new UUID is created by APNs and returned in the response.
	HeaderApnsID = "apns-id"

	// A UNIX epoch date expressed in seconds (UTC). This header identifies the date when the notification
	// is no longer valid and can be discarded.
	// If this value is nonzero, APNs stores the notification and tries to deliver it at least once, repeating
	// the attempt as needed if it is unable to deliver the notification the first time. If the value is 0,
	// APNs treats the notification as if it expires immediately and does not store the notification or attempt
	// to redeliver it.
	HeaderApnsExpiration = "apns-expiration"

	// The priority of the notification. Specify one of the following values:
	// 10–Send the push message immediately. Notifications with this priority must trigger an alert, sound,
	// or badge on the target device. It is an error to use this priority for a push notification that
	// contains only the content-available key.
	// 5—Send the push message at a time that takes into account power considerations for the device.
	// Notifications with this priority might be grouped and delivered in bursts. They are throttled,
	// and in some cases are not delivered.
	// If you omit this header, the APNs server sets the priority to 10.
	HeaderApnsPriority = "apns-priority"

	// The topic of the remote notification, which is typically the bundle ID for your app.
	// The certificate you create in your developer account must include the capability for this topic.
	// If your certificate includes multiple topics, you must specify a value for this header.
	// If you omit this request header and your APNs certificate does not specify multiple topics,
	// the APNs server uses the certificate’s Subject as the default topic.
	// If you are using a provider token instead of a certificate, you must specify a value for this
	// request header. The topic you provide should be provisioned for the your team named in your developer account.
	HeaderApnsTopic = "apns-topic"

	// Multiple notifications with the same collapse identifier are displayed to the user as a single notification.
	// The value of this key must not exceed 64 bytes. For more information, see Quality of Service,
	// Store-and-Forward, and Coalesced Notifications.
	HeaderApnsCollapseID = "apns-collapse-id"

	// The value of this header must accurately reflect the contents of your notification’s payload.
	// If there’s a mismatch, or if the header is missing on required systems, APNs may return an error,
	// delay the delivery of the notification, or drop it altogether.
	HeaderApnsPushType = "apns-push-type"
)

type ApnsPushTypeType string

const (
	// Use the alert push type for notifications that trigger a user interaction—for example, an alert, badge, or sound.
	// If you set this push type, the apns-topic header field must use your app’s bundle ID as the topic.
	// For more information, see Generating a remote notification.
	// If the notification requires immediate action from the user, set notification priority to 10; otherwise use 5.
	ApnsPushTypeAlert ApnsPushTypeType = "alert"

	// Use the background push type for notifications that deliver content in the background, and don’t trigger any user interactions.
	// If you set this push type, the apns-topic header field must use your app’s bundle ID as the topic. Always use priority 5.
	// Using priority 10 is an error. For more information, see Pushing Background Updates to Your App.
	ApnsPushTypeBackground ApnsPushTypeType = "background"

	// Use the location push type for notifications that request a user’s location. If you set this push type,
	// the apns-topic header field must use your app’s bundle ID with .location-query appended to the end.
	// If the location query requires an immediate response from the Location Push Service Extension, set notification
	// apns-priority to 10; otherwise, use 5. The location push type supports only token-based authentication.
	ApnsPushTypeLocation ApnsPushTypeType = "location"

	// Use the voip push type for notifications that provide information about an incoming Voice-over-IP (VoIP) call.
	// For more information, see Responding to VoIP Notifications from PushKit.
	// If you set this push type, the apns-topic header field must use your app’s bundle ID with .voip appended to the end.
	// If you’re using certificate-based authentication, you must also register the certificate for VoIP services.
	// The topic is then part of the 1.2.840.113635.100.6.3.4 or 1.2.840.113635.100.6.3.6 extension.
	ApnsPushTypeVoip ApnsPushTypeType = "voip"

	// Use the fileprovider push type to signal changes to a File Provider extension. If you set this push type,
	// the apns-topic header field must use your app’s bundle ID with .pushkit.fileprovider appended to the end.
	// For more information, see Using Push Notifications to Signal Changes.
	ApnsPushTypeFileprovider ApnsPushTypeType = "fileprovider"
)

// aps is the APNS payload. See explanation here:
// https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server/generating_a_remote_notification#2943363
type aps struct {
	Alert             *apsAlert             `json:"alert,omitempty"`
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

// PrepareLegacyNotifications creates notification payloads ready to be posted
// to legacy push notification server for the provided receipt.
func PrepareLegacyNotifications(rcpt *push.Receipt, config *CommonConfig) []MessageData {
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
			titlelc = config.getStringField(rcpt.Payload.What, "TitleLocKey")
			title = config.getStringField(rcpt.Payload.What, "Title")
			bodylc = config.getStringField(rcpt.Payload.What, "BodyLocKey")
			body = config.getStringField(rcpt.Payload.What, "Body")
			if body == "$content" {
				body = data["content"]
			}
			icon = config.getStringField(rcpt.Payload.What, "Icon")
			color = config.getStringField(rcpt.Payload.What, "Color")
			clickAction = config.getStringField(rcpt.Payload.What, "ClickAction")
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

	var messages []*fcmv1.Message
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
					if config.Webpush != nil && config.Webpush.Enabled {
						msg.Webpush = &fcmv1.WebpushConfig{}
					}
				case "":
					// ignore
				default:
					logs.Warn.Println("fcm: unknown device platform", d.Platform)
				}

				x, _ := json.Marshal(msg)
				logs.Err.Println("msg pushed:", string(x))
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

func androidNotificationConfig(what, topic string, data map[string]string, config *configType) *fcmv1.AndroidConfig {
	timeToLive := strconv.Itoa(config.TimeToLive) + "s"

	if what == push.ActRead {
		return &fcmv1.AndroidConfig{
			Priority:     "NORMAL",
			Notification: nil,
			Ttl:          timeToLive,
		}
	}

	_, videoCall := data["webrtc"]
	if videoCall {
		timeToLive = "0s"
	}

	// Priority "NORMAL" or "HIGH"
	priority := "HIGH"
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

	body := config.Android.getStringField(what, "Body")
	if body == "$content" {
		body = data["content"]
	}

	priority = "PRIORITY_HIGH"
	if videoCall {
		priority = "PRIORITY_MAX"
	}

	ac.Notification = &fcmv1.AndroidNotification{
		// Android uses Tag value to group notifications together:
		// show just one notification per topic.
		Tag:                  topic,
		NotificationPriority: priority,
		Visibility:           "PRIVATE",
		TitleLocKey:          config.Android.getStringField(what, "TitleLocKey"),
		Title:                config.Android.getStringField(what, "Title"),
		BodyLocKey:           config.Android.getStringField(what, "BodyLocKey"),
		Body:                 body,
		Icon:                 config.Android.getStringField(what, "Icon"),
		Color:                config.Android.getStringField(what, "Color"),
		ClickAction:          config.Android.getStringField(what, "ClickAction"),
	}

	return ac
}

func apnsNotificationConfig(what, topic string, data map[string]string, unread int, config *configType) *fcmv1.ApnsConfig {
	callStatus := data["webrtc"]
	expires := time.Now().UTC().Add(time.Duration(config.TimeToLive) * time.Second)
	bundleId := config.ApnsBundleID
	pushType := ApnsPushTypeAlert
	priority := 10
	interruptionLevel := InterruptionLevelTimeSensitive
	if callStatus == "started" {
		// Send VOIP push only when a new call is started, otherwise send normal alert.
		interruptionLevel = InterruptionLevelCritical
		pushType = ApnsPushTypeVoip
		bundleId += ".voip"
		expires = time.Now().UTC().Add(time.Duration(voipTimeToLive) * time.Second)
	} else if what == push.ActRead {
		priority = 5
		interruptionLevel = InterruptionLevelPassive
		pushType = ApnsPushTypeBackground
	}

	apsPayload := aps{
		Badge:             unread,
		ContentAvailable:  1,
		MutableContent:    1,
		InterruptionLevel: interruptionLevel,
		ThreadID:          topic,
	}

	if config.Apns != nil && config.Apns.Enabled && what != push.ActRead {
		body := config.Apns.getStringField(what, "Body")
		if body == "$content" {
			body = data["content"]
		}

		apsPayload.Alert = &apsAlert{
			Action:          config.Apns.getStringField(what, "Action"),
			ActionLocKey:    config.Apns.getStringField(what, "ActionLocKey"),
			Body:            body,
			LaunchImage:     config.Apns.getStringField(what, "LaunchImage"),
			LocKey:          config.Apns.getStringField(what, "LocKey"),
			Title:           config.Apns.getStringField(what, "Title"),
			Subtitle:        config.Apns.getStringField(what, "Subtitle"),
			TitleLocKey:     config.Apns.getStringField(what, "TitleLocKey"),
			SummaryArg:      config.Apns.getStringField(what, "SummaryArg"),
			SummaryArgCount: config.Apns.getIntField(what, "SummaryArgCount"),
		}
	} else {
		logs.Info.Println("no aps.alert:", config.Apns != nil, config.Apns != nil && config.Apns.Enabled, what != push.ActRead)
	}

	payload, err := json.Marshal(map[string]interface{}{"aps": apsPayload})
	if err != nil {
		return nil
	}
	headers := map[string]string{
		HeaderApnsExpiration: strconv.FormatInt(expires.Unix(), 10),
		HeaderApnsPriority:   strconv.Itoa(priority),
		HeaderApnsTopic:      bundleId,
		HeaderApnsCollapseID: topic,
		HeaderApnsPushType:   string(pushType),
	}

	ac := &fcmv1.ApnsConfig{
		Headers: headers,
		Payload: payload,
	}

	return ac
}
