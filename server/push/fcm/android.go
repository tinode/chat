package fcm

import (
	fcm "firebase.google.com/go/messaging"
)

// assignAndroidNotification assigns values to android-specific section of fcm.Message using payload
// values prepared by androidPrepareValues.
func assignAndroidNotification(msg *fcm.Message, values *preparedValues, tag, priority string) {
	msg.Android = &fcm.AndroidConfig{
		Priority: priority,
	}
	// When this notification type is included and the app is not in the foreground
	// Android won't wake up the app and won't call FirebaseMessagingService:onMessageReceived.
	// See dicussion: https://github.com/firebase/quickstart-js/issues/71
	if values != nil {
		msg.Android.Notification = &fcm.AndroidNotification{
			// Android uses Tag value to group notifications together:
			// show just one notification per topic.
			Tag:         tag,
			Priority:    fcm.PriorityHigh,
			Visibility:  fcm.VisibilityPrivate,
			TitleLocKey: values.titlelc,
			Title:       values.title,
			BodyLocKey:  values.bodylc,
			Body:        values.body,
			Icon:        values.icon,
			Color:       values.color,
			ClickAction: values.clickAction,
		}
	}
}
