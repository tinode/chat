package fcm

import (
	"strconv"
	"time"

	fcm "firebase.google.com/go/messaging"
)

// import "github.com/sideshow/apns2"

// assignAppleNotification assigns values to APNS-specific section of fcm.Message using payload
// values prepared by prepareValues.
func assignAppleNotification(msg *fcm.Message, values *preparedValues, tag, priority string, unread int) {
	if priority == "high" {
		priority = "10"
	} else {
		priority = "5"
	}

	msg.APNS = &fcm.APNSConfig{
		Headers: map[string]string{
			"apns-priority":   priority,
			"apns-expiration": strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
			"apns-topic":      "FIXME bundle-id",
			// Tag value to group notifications together:
			// show just one notification per topic.
			"apns-collapse-id": tag,
		},
	}
	if values != nil && !values.isVideoCall {
		msg.APNS.Payload = &fcm.APNSPayload{
			Aps: &fcm.Aps{
				ContentAvailable: true,
				MutableContent:   true,
				Sound:            "default",
				Category:         "FIX_ME",
				Alert: &fcm.ApsAlert{
					Title:        values.title,
					TitleLocKey:  values.titlelc,
					Body:         values.body,
					LocKey:       values.bodylc,
					ActionLocKey: values.actionlc,
				},
			},
		}
		if unread >= 0 {
			// iOS uses Badge to show the total unread message count.
			msg.APNS.Payload.Aps.Badge = &unread
		}
	}
}
