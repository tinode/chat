/******************************************************************************
 *
 *  Description :
 *
 *    Endpoints called by 8club's backend (CoS) rather than by chat clients.
 *    CoS calls them with the API key and its root user's login, the same
 *    credentials it uses for websocket and gRPC.
 *
 *****************************************************************************/

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Request bodies of the internal endpoints are small JSON objects.
	maxInternalRequestSize = 1 << 12

	// The 'what' of the {info} announcing a changed avatar.
	infoWhatAvatar = "avatar"
	// The 'type' of that {info}'s payload.
	avatarUpdatedPayloadType = "profile.avatar_updated"
)

// avatarUpdate is the body CoS posts after a user's visible avatar changed.
type avatarUpdate struct {
	// User's chat ID, 'usrXXX'.
	ChatID string `json:"chat_id"`
	// User's ID in CoS.
	UserID int64 `json:"user_id"`
	// Increases with every change of the user's avatar.
	AvatarVersion int `json:"avatar_version"`
}

// avatarUpdatedPayload is the payload of {info what="avatar"}.
type avatarUpdatedPayload struct {
	Type          string `json:"type"`
	UserID        int64  `json:"user_id"`
	AvatarVersion int    `json:"avatar_version"`
}

// serveAvatarUpdated handles CoS's report of a changed avatar: it validates the report, answers
// right away and then announces the change on the 'me' topic of everyone who chats with the user.
func serveAvatarUpdated(wrt http.ResponseWriter, req *http.Request) {
	now := types.TimeNow()

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		json.NewEncoder(wrt).Encode(msg)

		if err != nil {
			logs.Info.Println("avatar update:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
		}
	}

	if req.Method != http.MethodPost {
		writeHttpResponse(ErrOperationNotAllowed("", "", now), nil)
		return
	}

	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now), nil)
		return
	}

	authMethod, secret := getHttpAuth(req)
	if authMethod == "" {
		writeHttpResponse(ErrAuthRequired("", "", now, now), nil)
		return
	}
	rec, err := authHttpRequest(authMethod, secret, getRemoteAddr(req))
	if err != nil {
		writeHttpResponse(ErrAuthFailed("", "", now, now), err)
		return
	}
	if rec.AuthLevel != auth.LevelRoot {
		writeHttpResponse(ErrPermissionDenied("", "", now), nil)
		return
	}

	var update avatarUpdate
	if err := json.NewDecoder(http.MaxBytesReader(wrt, req.Body, maxInternalRequestSize)).Decode(&update); err != nil {
		writeHttpResponse(ErrMalformed("", "", now), err)
		return
	}

	uid := types.ParseUserId(update.ChatID)
	if uid.IsZero() || update.UserID <= 0 || update.AvatarVersion <= 0 {
		writeHttpResponse(ErrMalformed("", "", now), nil)
		return
	}

	payload, _ := json.Marshal(&avatarUpdatedPayload{
		Type:          avatarUpdatedPayloadType,
		UserID:        update.UserID,
		AvatarVersion: update.AvatarVersion,
	})

	// Finding the recipients reads every chat the user is in, CoS does not need to wait for it.
	go announceAvatarUpdated(uid, payload)

	writeHttpResponse(NoErrAccepted("", "", now), nil)
}

// authHttpRequest logs in with the request's credentials, e.g. 'X-Tinode-Auth: Basic <secret>',
// where the secret is base64-encoded as in {login}. Multi-step logins are not supported.
func authHttpRequest(authMethod, secret, remoteAddr string) (*auth.Rec, error) {
	decodedSecret, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, types.ErrMalformed
	}

	authhdl := store.Store.GetLogicalAuthHandler(authMethod)
	if authhdl == nil {
		return nil, types.ErrMalformed
	}

	rec, challenge, err := authhdl.Authenticate(decodedSecret, remoteAddr)
	if err != nil {
		return nil, err
	}
	if challenge != nil {
		return nil, types.ErrFailed
	}
	return rec, nil
}

// announceAvatarUpdated sends {info what="avatar"} to the 'me' topic of the user's chat peers,
// the members of the user's group chats and the user's own sessions. Users who are offline
// miss it, clients compare avatar versions when they load profiles.
func announceAvatarUpdated(uid types.Uid, payload json.RawMessage) {
	recipients, err := avatarUpdateRecipients(uid)
	if err != nil {
		logs.Warn.Println("avatar update: failed to load chats of", uid.UserId(), err)
	}
	// User's own sessions on other devices.
	recipients[uid] = true

	user := uid.UserId()
	for rcpt := range recipients {
		globals.hub.routeSrv <- &ServerComMessage{
			Info: &MsgServerInfo{
				Topic:   "me",
				Src:     user,
				From:    user,
				What:    infoWhatAvatar,
				Payload: payload,
			},
			RcptTo: rcpt.UserId(),
		}
	}
}

// avatarUpdateRecipients returns everyone who sees the user in a p2p or group chat. Both the user
// and the recipient need the R and P permissions in that chat, which also leaves out users who
// blocked one another: a block takes P away from the blocked side.
func avatarUpdateRecipients(uid types.Uid) (map[types.Uid]bool, error) {
	recipients := make(map[types.Uid]bool)

	subs, err := store.Users.GetSubs(uid)
	if err != nil {
		return recipients, err
	}

	for i := range subs {
		topic := subs[i].Topic
		if cat := types.GetTopicCat(topic); (cat != types.TopicCatP2P && cat != types.TopicCatGrp) ||
			types.IsChannel(topic) {
			continue
		}
		if !seesAvatarUpdates(&subs[i]) {
			continue
		}

		members, err := store.Topics.GetSubs(topic, nil)
		if err != nil {
			logs.Warn.Println("avatar update: failed to load members of", topic, err)
			continue
		}
		for j := range members {
			member := types.ParseUid(members[j].User)
			if member.IsZero() || member == uid || !seesAvatarUpdates(&members[j]) {
				continue
			}
			recipients[member] = true
		}
	}

	return recipients, nil
}

// seesAvatarUpdates checks the R and P permissions of a subscription, the same ones
// {info} notifications on 'me' require.
func seesAvatarUpdates(sub *types.Subscription) bool {
	mode := sub.ModeGiven & sub.ModeWant
	return mode.IsReader() && mode.IsPresencer()
}
