package main

import (
	"log"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Process request for a new account.
func replyCreateUser(s *Session, msg *ClientComMessage, rec *auth.Rec) {
	// The session cannot authenticate with the new account because  it's already authenticated.
	if msg.Acc.Login && (!s.uid.IsZero() || rec != nil) {
		s.queueOut(ErrAlreadyAuthenticated(msg.id, "", msg.timestamp))
		log.Println("s.acc: login requested while authenticated", s.sid)
		return
	}

	authhdl := store.GetLogicalAuthHandler(msg.Acc.Scheme)
	if authhdl == nil {
		// New accounts must have an authentication scheme
		s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
		log.Println("s.acc: unknown auth handler", s.sid)
		return
	}

	// Check if login is unique.
	if ok, err := authhdl.IsUnique(msg.Acc.Secret); !ok {
		log.Println("s.acc: auth secret is not unique", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp,
			map[string]interface{}{"what": "auth"}))
		return
	}

	var user types.User
	var private interface{}

	// Assign default access values in case the acc creator has not provided them
	user.Access.Auth = getDefaultAccess(types.TopicCatP2P, true)
	user.Access.Anon = getDefaultAccess(types.TopicCatP2P, false)

	if tags := normalizeTags(msg.Acc.Tags); tags != nil {
		if !restrictedTagsEqual(tags, nil, globals.immutableTagNS) {
			log.Println("a.acc: attempt to directly assign restricted tags", s.sid)
			msg := ErrPermissionDenied(msg.id, "", msg.timestamp)
			msg.Ctrl.Params = map[string]interface{}{"what": "tags"}
			s.queueOut(msg)
			return
		}
		user.Tags = tags
	}

	// Pre-check credentials for validity. We don't know user's access level
	// consequently cannot check presence of required credentials. Must do that later.
	creds := normalizeCredentials(msg.Acc.Cred, true)
	for i := range creds {
		cr := &creds[i]
		vld := store.GetValidator(cr.Method)
		if err := vld.PreCheck(cr.Value, cr.Params); err != nil {
			log.Println("a.acc: failed credential pre-check", cr, err, s.sid)
			s.queueOut(decodeStoreError(err, msg.Acc.Id, "", msg.timestamp,
				map[string]interface{}{"what": cr.Method}))
			return
		}
	}

	if msg.Acc.Desc != nil {
		if msg.Acc.Desc.DefaultAcs != nil {
			if msg.Acc.Desc.DefaultAcs.Auth != "" {
				user.Access.Auth.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Auth))
				user.Access.Auth &= types.ModeCP2P
				if user.Access.Auth != types.ModeNone {
					user.Access.Auth |= types.ModeApprove
				}
			}
			if msg.Acc.Desc.DefaultAcs.Anon != "" {
				user.Access.Anon.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Anon))
				user.Access.Anon &= types.ModeCP2P
				if user.Access.Anon != types.ModeNone {
					user.Access.Anon |= types.ModeApprove
				}
			}
		}
		if !isNullValue(msg.Acc.Desc.Public) {
			user.Public = msg.Acc.Desc.Public
		}
		if !isNullValue(msg.Acc.Desc.Private) {
			private = msg.Acc.Desc.Private
		}
	}

	if _, err := store.Users.Create(&user, private); err != nil {
		log.Println("s.acc: failed to create user", err, s.sid)
		s.queueOut(ErrUnknown(msg.id, "", msg.timestamp))
		return
	}

	rec, err := authhdl.AddRecord(&auth.Rec{Uid: user.Uid(), Tags: user.Tags}, msg.Acc.Secret)
	if err != nil {
		log.Println("s.acc: add auth record failed", err, s.sid)
		// Attempt to delete incomplete user record
		store.Users.Delete(user.Uid(), false)
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
		return
	}

	// When creating an account, the user must provide all required credentials.
	// If any are missing, reject the request.
	if len(creds) < len(globals.authValidators[rec.AuthLevel]) {
		log.Println("s.acc: missing credentials; have:", creds, "want:", globals.authValidators[rec.AuthLevel], s.sid)
		// Attempt to delete incomplete user record
		store.Users.Delete(user.Uid(), false)
		_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], credentialMethods(creds))
		s.queueOut(decodeStoreError(types.ErrPolicy, msg.id, "", msg.timestamp,
			map[string]interface{}{"creds": missing}))
		return
	}

	var validated []string
	tmpToken, _, _ := store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
		Uid:       user.Uid(),
		AuthLevel: auth.LevelNone,
		Lifetime:  time.Hour * 24,
		Features:  auth.FeatureNoLogin})
	for i := range creds {
		cr := &creds[i]
		vld := store.GetValidator(cr.Method)
		if err := vld.Request(user.Uid(), cr.Value, s.lang, cr.Response, tmpToken); err != nil {
			log.Println("s.acc: failed to save or validate credential", err, s.sid)
			// Delete incomplete user record.
			store.Users.Delete(user.Uid(), false)
			s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp,
				map[string]interface{}{"what": cr.Method}))
			return
		}

		if cr.Response != "" {
			// If response is provided and Request did not return an error, the request was
			// successfully validated.
			validated = append(validated, cr.Method)

			// Generate tags for these confirmed credentials.
			if globals.validators[cr.Method].addToTags {
				rec.Tags = append(rec.Tags, cr.Method+":"+cr.Value)
			}
		}
	}

	// Save tags potentially changed by the validator.
	if err := store.Users.Update(user.Uid(),
		map[string]interface{}{"Tags": types.StringSlice(rec.Tags)}); err != nil {

		log.Println("s.acc: failed to update user's tags", err, s.sid)
		// Delete incomplete user record.
		store.Users.Delete(user.Uid(), false)
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
		return
	}

	var reply *ServerComMessage
	if msg.Acc.Login {
		// Process user's login request.
		_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], validated)
		reply = s.onLogin(msg.id, msg.timestamp, rec, missing)
	} else {
		// Not using the new account for logging in.
		reply = NoErrCreated(msg.id, "", msg.timestamp)
		reply.Ctrl.Params = map[string]interface{}{
			"user":    user.Uid().UserId(),
			"authlvl": rec.AuthLevel.String(),
		}
	}
	params := reply.Ctrl.Params.(map[string]interface{})
	params["desc"] = &MsgTopicDesc{
		CreatedAt: &user.CreatedAt,
		UpdatedAt: &user.UpdatedAt,
		DefaultAcs: &MsgDefaultAcsMode{
			Auth: user.Access.Auth.String(),
			Anon: user.Access.Anon.String()},
		Public:  user.Public,
		Private: private}

	s.queueOut(reply)

	pluginAccount(&user, plgActCreate)
}

// Process update to an account:
// * Authentication update, i.e. password change
// * Credentials update
func replyUpdateUser(s *Session, msg *ClientComMessage, rec *auth.Rec) {
	if s.uid.IsZero() && rec == nil {
		// Session is not authenticated and no token provided.
		log.Println("replyUpdateUser: not a new account and not authenticated", s.sid)
		s.queueOut(ErrPermissionDenied(msg.id, "", msg.timestamp))
		return
	} else if msg.from != "" && rec != nil {
		// Two UIDs: one from msg.from, one from token. Ambigous, reject.
		log.Println("replyUpdateUser: got both authenticated session and token", s.sid)
		s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
		return
	}

	userId := msg.from
	authLvl := auth.Level(msg.authLvl)
	if rec != nil {
		userId = rec.Uid.UserId()
		authLvl = rec.AuthLevel
	}
	if msg.Acc.User != "" && msg.Acc.User != userId {
		if s.authLvl != auth.LevelRoot {
			log.Println("replyUpdateUser: attempt to change another's account by non-root", s.sid)
			s.queueOut(ErrPermissionDenied(msg.id, "", msg.timestamp))
			return
		}
		// Root is editing someone else's account.
		userId = msg.Acc.User
		authLvl = auth.ParseAuthLevel(msg.Acc.AuthLevel)
	}

	uid := types.ParseUserId(userId)
	if uid.IsZero() || authLvl == auth.LevelNone {
		// Either msg.Acc.User or msg.Acc.AuthLevel contains invalid data.
		s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
		log.Println("replyUpdateUser: either user id or auth level is missing", s.sid)
		return
	}

	user, err := store.Users.Get(uid)
	if user == nil && err == nil {
		err = types.ErrNotFound
	}
	if err != nil {
		log.Println("replyUpdateUser: failed to fetch user from DB", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
		return
	}

	var params map[string]interface{}
	authhdl := store.GetLogicalAuthHandler(msg.Acc.Scheme)
	if authhdl != nil {
		// Request to update auth of an existing account. Only basic & rest auth are currently supported

		// TODO(gene): support adding new auth schemes

		rec, err := authhdl.UpdateRecord(&auth.Rec{Uid: uid, Tags: user.Tags}, msg.Acc.Secret)
		if err != nil {
			log.Println("replyUpdateUser: failed to update auth secret", err, s.sid)
			s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
			return
		}

		// Tags may have changed, update them
		store.Users.UpdateTags(uid, rec.Tags, true)

	} else if msg.Acc.Scheme != "" {
		// Invalid or unknown auth scheme
		log.Println("replyUpdateUser: unknown auth scheme", msg.Acc.Scheme, s.sid)
		s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
		return
	} else if len(msg.Acc.Cred) > 0 {
		// Use provided credentials for validation.
		validated, err := s.getValidatedGred(uid, authLvl, msg.Acc.Cred)
		if err != nil {
			log.Println("replyUpdateUser: failed to get validated credentials", err, s.sid)
			s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
			return
		}
		_, missing := stringSliceDelta(globals.authValidators[authLvl], validated)
		if len(missing) > 0 {
			params = map[string]interface{}{"cred": missing}
		}
	}

	resp := NoErr(msg.id, "", msg.timestamp)
	resp.Ctrl.Params = params
	s.queueOut(resp)

	// Call plugin with the account update
	pluginAccount(user, plgActUpd)
}

// Request to delete a user:
// 1. Disable user's login
// 2. Terminate all user's sessions except the current session.
// 3. Stop all active topics
// 4. Notify other subscribers that topics are being deleted.
// 5. Delete user from the database.
// 6. Report success or failure.
// 7. Terminate user's last session.
func replyDelUser(s *Session, msg *ClientComMessage) {
	var reply *ServerComMessage
	var uid types.Uid

	if msg.Del.User == "" || msg.Del.User == s.uid.UserId() {
		// Delete current user.
		uid = s.uid
	} else if s.authLvl == auth.LevelRoot {
		// Delete another user.
		uid = types.ParseUserId(msg.Del.User)
		if uid.IsZero() {
			reply = ErrMalformed(msg.id, "", msg.timestamp)
			log.Println("replyDelUser: invalid user ID", msg.Del.User, s.sid)
		}
	} else {
		reply = ErrPermissionDenied(msg.id, "", msg.timestamp)
		log.Println("replyDelUser: illegal attempt to delete another user", msg.Del.User, s.sid)
	}

	if reply == nil {
		// Disable all authenticators
		authnames := store.GetAuthNames()
		for _, name := range authnames {
			if err := store.GetAuthHandler(name).DelRecords(uid); err != nil {
				// This could be completely benighn, i.e. authenticator exists but not used.
				log.Println("replyDelUser: failed to delete auth record", uid.UserId(), name, err, s.sid)
			}
		}

		// Terminate all sessions. Skip the current session so the requester gets a response.
		globals.sessionStore.EvictUser(uid, s.sid)

		// Stop topics where the user is the owner and p2p topics.
		done := make(chan bool)
		globals.hub.unreg <- &topicUnreg{forUser: uid, del: msg.Del.Hard, done: done}
		<-done

		// Notify users of interest that the user is gone.
		if uoi, err := store.Users.GetSubs(uid, nil); err == nil {
			log.Println("notifying users of interest", uoi)
			presUsersOfInterestOffline(uid, uoi, "gone")
		} else {
			log.Println("replyDelUser: failed to send notifications to users", err, s.sid)
		}

		// Notify subscribers of the group topics where the user was the owner that the topics were deleted.
		if ownTopics, err := store.Users.GetOwnTopics(uid, nil); err == nil {
			log.Println("deleting owned topics", ownTopics)
			for _, topicName := range ownTopics {
				if subs, err := store.Topics.GetSubs(topicName, nil); err == nil {
					presSubsOfflineOffline(topicName, types.TopicCatGrp, subs, "gone", &presParams{}, s.sid)
				}
			}
		} else {
			log.Println("replyDelUser: failed to send notifications to owned topics", err, s.sid)
		}

		// Delete user's records from the database.
		if err := store.Users.Delete(uid, msg.Del.Hard); err != nil {
			reply = decodeStoreError(err, msg.id, "", msg.timestamp, nil)
			log.Println("replyDelUser: failed to delete user", err, s.sid)
		} else {
			reply = NoErr(msg.id, "", msg.timestamp)
		}
	}

	s.queueOut(reply)

	if s.uid == uid {
		// Evict the current session if it belongs to the deleted user.
		s.stop <- s.serialize(NoErrEvicted("", "", msg.timestamp))
	}
}
