package common

import (
	"reflect"

	"github.com/tinode/chat/server/push"
)

// Payload to be sent for a specific notification type.
type Payload struct {
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

// Config is the configuration of a Notification payload.
type Config struct {
	Enabled bool `json:"enabled,omitempty"`
	// Common defaults for all push types.
	Payload
	// Configs for specific push types.
	Msg Payload `json:"msg,omitempty"`
	Sub Payload `json:"sub,omitempty"`
}

func (cp Payload) getStringAttr(field string) string {
	val := reflect.ValueOf(cp).FieldByName(field)
	if !val.IsValid() {
		return ""
	}
	if val.Kind() == reflect.String {
		return val.String()
	}
	return ""
}

func (cp Payload) getIntAttr(field string) int {
	val := reflect.ValueOf(cp).FieldByName(field)
	if !val.IsValid() {
		return 0
	}
	if val.Kind() == reflect.Int {
		return int(val.Int())
	}
	return 0
}

func (cc *Config) GetStringField(what, field string) string {
	var val string
	if what == push.ActMsg {
		val = cc.Msg.getStringAttr(field)
	} else if what == push.ActSub {
		val = cc.Sub.getStringAttr(field)
	}
	if val == "" {
		val = cc.Payload.getStringAttr(field)
	}
	return val
}

func (cc *Config) GetIntField(what, field string) int {
	var val int
	if what == push.ActMsg {
		val = cc.Msg.getIntAttr(field)
	} else if what == push.ActSub {
		val = cc.Sub.getIntAttr(field)
	}
	if val == 0 {
		val = cc.Payload.getIntAttr(field)
	}
	return val
}

// AndroidVisibilityType defines notification visibility constants https://developer.android.com/reference/android/app/Notification.html#visibility
type AndroidVisibilityType string

const (
	// If unspecified, default to `Visibility.PRIVATE`.
	AndroidVisibilityUnspecified AndroidVisibilityType = "VISIBILITY_UNSPECIFIED"

	// Show this notification on all lockscreens, but conceals
	// sensitive or private information on secure lockscreens.
	AndroidVisibilityPrivate AndroidVisibilityType = "PRIVATE"

	// Show this notification in its entirety on all lockscreens.
	AndroidVisibilityPublic AndroidVisibilityType = "PUBLIC"

	// Do not reveal any part of this notification on a secure
	AndroidVisibilitySecret AndroidVisibilityType = "SECRET"
)

// AndroidNotificationPriorityType defines notification priority consumeed by the client
// after it receives the notification. Does not affect FCM sending.
type AndroidNotificationPriorityType string

const (
	// If priority is unspecified, notification priority is set to `PRIORITY_DEFAULT`.
	AndroidNotificationPriorityUnspecified AndroidNotificationPriorityType = "PRIORITY_UNSPECIFIED"

	// Lowest notification priority. Notifications with this `PRIORITY_MIN` might not be
	// shown to the user except under special circumstances, such as detailed notification logs.
	AndroidNotificationPriorityMin AndroidNotificationPriorityType = "PRIORITY_MIN"

	// Lower notification priority. The UI may choose to show the notifications smaller,
	// or at a different position in the list, compared with notifications with `PRIORITY_DEFAULT`.
	AndroidNotificationPriorityLow AndroidNotificationPriorityType = "PRIORITY_LOW"

	// Default notification priority. If the application does not prioritize its own notifications,
	// use this value for all notifications.
	AndroidNotificationPriorityDefault AndroidNotificationPriorityType = "PRIORITY_DEFAULT"

	// Higher notification priority. Use this for more important notifications or alerts.
	// The UI may choose to show these notifications larger, or at a different position in the notification
	// lists, compared with notifications with `PRIORITY_DEFAULT`.
	AndroidNotificationPriorityHigh AndroidNotificationPriorityType = "PRIORITY_HIGH"

	// Highest notification priority. Use this for the application's most important items that
	// require the user's prompt attention or input.
	AndroidNotificationPriorityMax AndroidNotificationPriorityType = "PRIORITY_MAX"
)

// AndroidPriorityType defines the server-side priorities https://goo.gl/GjONJv. It affects how soon
// FCM sends the push.
type AndroidPriorityType string

const (
	// Default priority for data messages. Normal priority messages won't open network
	// connections on a sleeping device, and their delivery may be delayed to conserve
	// the battery. For less time-sensitive messages, such as notifications of new email
	// or other data to sync, choose normal delivery priority.
	AndroidPriorityNormal AndroidPriorityType = "NORMAL"

	// Default priority for notification messages. FCM attempts to deliver high priority
	// messages immediately, allowing the FCM service to wake a sleeping device when possible
	// and open a network connection to your app server. Apps with instant messaging, chat,
	// or voice call alerts, for example, generally need to open a network connection and make
	// sure FCM delivers the message to the device without delay. Set high priority if the message
	// is time-critical and requires the user's immediate interaction, but beware that setting
	// your messages to high priority contributes more to battery drain compared with normal priority messages.
	AndroidPriorityHigh AndroidPriorityType = "HIGH"
)

// InterruptionLevelType defines the values for the APNS payload.aps.InterruptionLevel.
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

// Aps is the APNS payload. See explanation here:
// https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server/generating_a_remote_notification#2943363
type Aps struct {
	Alert             *ApsAlert             `json:"alert,omitempty"`
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

// ApsAlert is the content of the aps.Alert field.
type ApsAlert struct {
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
