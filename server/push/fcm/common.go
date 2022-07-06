package fcm

import "github.com/tinode/chat/server/push"

// Payload values configured for a specific push message.
type preparedValues struct {
	isVideoCall bool
	title       string
	body        string
	titlelc     string
	bodylc      string
	icon        string
	color       string
	clickAction string
	actionlc    string
}

// Default platform-specific configuration for one message type.
type notificationConfig struct {
	TitleLocKey string `json:"title_loc_key,omitempty"`
	Title       string `json:"title,omitempty"`
	BodyLocKey  string `json:"body_loc_key,omitempty"`
	Body        string `json:"body,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	// Android
	ClickAction string `json:"click_action,omitempty"`
	// Apple
	ActionLocKey string `json:"action_loc_key,omitempty"`
}

func (ac *CommonNotifConfig) getTitleLocKey(what string) string {
	var title string
	if what == push.ActMsg {
		title = ac.Msg.TitleLocKey
	} else if what == push.ActSub {
		title = ac.Sub.TitleLocKey
	}
	if title == "" {
		title = ac.notificationConfig.TitleLocKey
	}
	return title
}

func (ac *CommonNotifConfig) getTitle(what string) string {
	var title string
	if what == push.ActMsg {
		title = ac.Msg.Title
	} else if what == push.ActSub {
		title = ac.Sub.Title
	}
	if title == "" {
		title = ac.notificationConfig.Title
	}
	return title
}

func (ac *CommonNotifConfig) getBodyLocKey(what string) string {
	var body string
	if what == push.ActMsg {
		body = ac.Msg.BodyLocKey
	} else if what == push.ActSub {
		body = ac.Sub.BodyLocKey
	}
	if body == "" {
		body = ac.notificationConfig.BodyLocKey
	}
	return body
}

func (ac *CommonNotifConfig) getBody(what string) string {
	var body string
	if what == push.ActMsg {
		body = ac.Msg.Body
	} else if what == push.ActSub {
		body = ac.Sub.Body
	}
	if body == "" {
		body = ac.notificationConfig.Body
	}
	return body
}

func (ac *CommonNotifConfig) getIcon(what string) string {
	var icon string
	if what == push.ActMsg {
		icon = ac.Msg.Icon
	} else if what == push.ActSub {
		icon = ac.Sub.Icon
	}
	if icon == "" {
		icon = ac.notificationConfig.Icon
	}
	return icon
}

func (ac *CommonNotifConfig) getColor(what string) string {
	var color string
	if what == push.ActMsg {
		color = ac.Msg.Color
	} else if what == push.ActSub {
		color = ac.Sub.Color
	}
	if color == "" {
		color = ac.notificationConfig.Color
	}
	return color
}

func (ac *CommonNotifConfig) getClickAction(what string) string {
	var clickAction string
	if what == push.ActMsg {
		clickAction = ac.Msg.ClickAction
	} else if what == push.ActSub {
		clickAction = ac.Sub.ClickAction
	}
	if clickAction == "" {
		clickAction = ac.notificationConfig.ClickAction
	}
	return clickAction
}

func (ac *CommonNotifConfig) getActionLocKey(what string) string {
	var actionlc string
	if what == push.ActMsg {
		actionlc = ac.Msg.ActionLocKey
	} else if what == push.ActSub {
		actionlc = ac.Sub.ActionLocKey
	}
	if actionlc == "" {
		actionlc = ac.notificationConfig.ActionLocKey
	}
	return actionlc
}

// prepareValues uses default config to get concrete values for specific push type and content.
func prepareValues(config *CommonNotifConfig, what string, data map[string]string) *preparedValues {
	if config == nil || !config.Enabled {
		// Config is missing or disabled.
		return nil
	}

	if what == push.ActRead {
		// No payload for READ notifications.
		return nil
	}

	_, isVideoCall := data["webrtc"]

	prepared := preparedValues{
		isVideoCall: isVideoCall,
		titlelc:     config.getTitleLocKey(what),
		title:       config.getTitle(what),
		bodylc:      config.getBodyLocKey(what),
		body:        config.getBody(what),
		icon:        config.getIcon(what),
		color:       config.getColor(what),
		clickAction: config.getClickAction(what),
		actionlc:    config.getActionLocKey(what),
	}
	if prepared.body == "$content" {
		prepared.body = data["content"]
	}
	return &prepared
}
