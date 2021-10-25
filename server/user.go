package main

import (
	"container/heap"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Unread counter update return codes.
	// Counter not initialized, IO pending.
	unreadUpdateIOPending = -1
	// Counter initialization error.
	unreadUpdateError = -2
)

// Process request for a new account.
func replyCreateUser(s *Session, msg *ClientComMessage, rec *auth.Rec) {
	// The session cannot authenticate with the new account because  it's already authenticated.
	if msg.Acc.Login && (!s.uid.IsZero() || rec != nil) {
		s.queueOut(ErrAlreadyAuthenticated(msg.Id, "", msg.Timestamp))
		logs.Warn.Println("create user: login requested while authenticated", s.sid)
		return
	}

	// Find authenticator for the requested scheme.
	authhdl := store.Store.GetLogicalAuthHandler(msg.Acc.Scheme)
	if authhdl == nil {
		// New accounts must have an authentication scheme
		s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
		logs.Warn.Println("create user: unknown auth handler", s.sid)
		return
	}

	// Check if login is unique.
	if ok, err := authhdl.IsUnique(msg.Acc.Secret, s.remoteAddr); !ok {
		logs.Warn.Println("create user: auth secret is not unique", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp,
			map[string]interface{}{"what": "auth"}))
		return
	}

	var user types.User
	var private interface{}

	// If account state is being assigned, make sure the sender is a root user.
	if msg.Acc.State != "" {
		if auth.Level(msg.AuthLvl) != auth.LevelRoot {
			logs.Warn.Println("create user: attempt to set account state by non-root", s.sid)
			msg := ErrPermissionDenied(msg.Id, "", msg.Timestamp)
			msg.Ctrl.Params = map[string]interface{}{"what": "state"}
			s.queueOut(msg)
			return
		}

		state, err := types.NewObjState(msg.Acc.State)
		if err != nil || state == types.StateUndefined || state == types.StateDeleted {
			logs.Warn.Println("create user: invalid account state", err, s.sid)
			s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
			return
		}
		user.State = state
	}

	// Ensure tags are unique and not restricted.
	if tags := normalizeTags(msg.Acc.Tags); tags != nil {
		if !restrictedTagsEqual(tags, nil, globals.immutableTagNS) {
			logs.Warn.Println("create user: attempt to directly assign restricted tags", s.sid)
			msg := ErrPermissionDenied(msg.Id, "", msg.Timestamp)
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
		vld := store.Store.GetValidator(cr.Method)
		if _, err := vld.PreCheck(cr.Value, cr.Params); err != nil {
			logs.Warn.Println("create user: failed credential pre-check", cr, err, s.sid)
			s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp,
				map[string]interface{}{"what": cr.Method}))
			return
		}
	}

	// Assign default access values in case the acc creator has not provided them
	user.Access.Auth = getDefaultAccess(types.TopicCatP2P, true, false) |
		getDefaultAccess(types.TopicCatGrp, true, false)
	user.Access.Anon = getDefaultAccess(types.TopicCatP2P, false, false) |
		getDefaultAccess(types.TopicCatGrp, false, false)

	// Assign actual access values, public and private.
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

	// Create user record in the database.
	if _, err := store.Users.Create(&user, private); err != nil {
		logs.Warn.Println("create user: failed to create user", err, s.sid)
		s.queueOut(ErrUnknown(msg.Id, "", msg.Timestamp))
		return
	}

	// Add authentication record. The authhdl.AddRecord may change tags.
	rec, err := authhdl.AddRecord(&auth.Rec{Uid: user.Uid(), Tags: user.Tags}, msg.Acc.Secret, s.remoteAddr)
	if err != nil {
		logs.Warn.Println("create user: add auth record failed", err, s.sid)
		// Attempt to delete incomplete user record
		store.Users.Delete(user.Uid(), false)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp, nil))
		return
	}

	// When creating an account, the user must provide all required credentials.
	// If any are missing, reject the request.
	if len(creds) < len(globals.authValidators[rec.AuthLevel]) {
		logs.Warn.Println("create user: missing credentials; have:", creds, "want:",
			globals.authValidators[rec.AuthLevel], s.sid)
		// Attempt to delete incomplete user record
		store.Users.Delete(user.Uid(), false)
		_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], credentialMethods(creds))
		s.queueOut(decodeStoreError(types.ErrPolicy, msg.Id, "", msg.Timestamp,
			map[string]interface{}{"creds": missing}))
		return
	}

	// Save credentials, update tags if necessary.
	tmpToken, _, _ := store.Store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
		Uid:       user.Uid(),
		AuthLevel: auth.LevelNone,
		Lifetime:  auth.Duration(time.Hour * 24),
		Features:  auth.FeatureNoLogin,
	})
	validated, _, err := addCreds(user.Uid(), creds, rec.Tags, s.lang, tmpToken)
	if err != nil {
		// Delete incomplete user record.
		store.Users.Delete(user.Uid(), false)
		logs.Warn.Println("create user: failed to save or validate credential", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp, nil))
		return
	}

	var reply *ServerComMessage
	if msg.Acc.Login {
		// Process user's login request.
		_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], validated)
		reply = s.onLogin(msg.Id, msg.Timestamp, rec, missing)
	} else {
		// Not using the new account for logging in.
		reply = NoErrCreated(msg.Id, "", msg.Timestamp)
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
			Anon: user.Access.Anon.String(),
		},
		Public:  user.Public,
		Private: private,
	}

	s.queueOut(reply)

	pluginAccount(&user, plgActCreate)
}

// Process update to an account:
// * Authentication update, i.e. login/password change
// * Credentials update
func replyUpdateUser(s *Session, msg *ClientComMessage, rec *auth.Rec) {
	if s.uid.IsZero() && rec == nil {
		// Session is not authenticated and no token provided.
		logs.Warn.Println("replyUpdateUser: not a new account and not authenticated", s.sid)
		s.queueOut(ErrPermissionDenied(msg.Id, "", msg.Timestamp))
		return
	} else if msg.AsUser != "" && rec != nil {
		// Two UIDs: one from msg.from, one from token. Ambigous, reject.
		logs.Warn.Println("replyUpdateUser: got both authenticated session and token", s.sid)
		s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
		return
	}

	userId := msg.AsUser
	authLvl := auth.Level(msg.AuthLvl)
	if rec != nil {
		userId = rec.Uid.UserId()
		authLvl = rec.AuthLevel
	}

	if msg.Acc.User != "" && msg.Acc.User != userId {
		if s.authLvl != auth.LevelRoot {
			logs.Warn.Println("replyUpdateUser: attempt to change another's account by non-root", s.sid)
			s.queueOut(ErrPermissionDenied(msg.Id, "", msg.Timestamp))
			return
		}
		// Root is editing someone else's account.
		userId = msg.Acc.User
		authLvl = auth.ParseAuthLevel(msg.Acc.AuthLevel)
	}

	uid := types.ParseUserId(userId)
	if uid.IsZero() {
		// msg.Acc.User contains invalid data.
		s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
		logs.Warn.Println("replyUpdateUser: user id is invalid or missing", s.sid)
		return
	}

	// Only root can suspend accounts, including own account.
	if msg.Acc.State != "" && s.authLvl != auth.LevelRoot {
		s.queueOut(ErrPermissionDenied(msg.Id, "", msg.Timestamp))
		logs.Warn.Println("replyUpdateUser: attempt to change account state by non-root", s.sid)
		return
	}

	user, err := store.Users.Get(uid)
	if user == nil && err == nil {
		err = types.ErrNotFound
	}
	if err != nil {
		logs.Warn.Println("replyUpdateUser: failed to fetch user from DB", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp, nil))
		return
	}

	var params map[string]interface{}
	if msg.Acc.Scheme != "" {
		err = updateUserAuth(msg, user, rec, s.remoteAddr)
	} else if len(msg.Acc.Cred) > 0 {
		if authLvl == auth.LevelNone {
			// msg.Acc.AuthLevel contains invalid data.
			s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
			logs.Warn.Println("replyUpdateUser: auth level is missing", s.sid)
			return
		}
		// Handle request to update credentials.
		tmpToken, _, _ := store.Store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
			Uid:       uid,
			AuthLevel: auth.LevelNone,
			Lifetime:  auth.Duration(time.Hour * 24),
			Features:  auth.FeatureNoLogin,
		})
		_, _, err := addCreds(uid, msg.Acc.Cred, nil, s.lang, tmpToken)
		if err == nil {
			if allCreds, err := store.Users.GetAllCreds(uid, "", true); err != nil {
				var validated []string
				for i := range allCreds {
					validated = append(validated, allCreds[i].Method)
				}
				_, missing := stringSliceDelta(globals.authValidators[authLvl], validated)
				if len(missing) > 0 {
					params = map[string]interface{}{"cred": missing}
				}
			}
		}
	} else if msg.Acc.State != "" {
		var changed bool
		changed, err = changeUserState(s, uid, user, msg)
		if !changed && err == nil {
			s.queueOut(InfoNotModified(msg.Id, "", msg.Timestamp))
			return
		}
	} else {
		err = types.ErrMalformed
	}

	if err != nil {
		logs.Warn.Println("replyUpdateUser: failed to update user", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp, nil))
		return
	}

	s.queueOut(NoErrParams(msg.Id, "", msg.Timestamp, params))

	// Call plugin with the account update
	pluginAccount(user, plgActUpd)
}

// Authentication update
func updateUserAuth(msg *ClientComMessage, user *types.User, rec *auth.Rec, remoteAddr string) error {
	authhdl := store.Store.GetLogicalAuthHandler(msg.Acc.Scheme)
	if authhdl != nil {
		// Request to update auth of an existing account. Only basic & rest auth are currently supported

		// TODO(gene): support adding new auth schemes

		rec, err := authhdl.UpdateRecord(&auth.Rec{Uid: user.Uid(), Tags: user.Tags}, msg.Acc.Secret, remoteAddr)
		if err != nil {
			return err
		}

		// Tags may have been changed by authhdl.UpdateRecord, reset them.
		// Can't do much with the error here, logging it but not returning.
		if _, err = store.Users.UpdateTags(user.Uid(), nil, nil, rec.Tags); err != nil {
			logs.Warn.Println("updateUserAuth tags update failed:", err)
		}
		return nil
	}

	// Invalid or unknown auth scheme
	return types.ErrMalformed
}

// addCreds adds new credentials and re-send validation request for existing ones.
// It also adds credential-defined tags if necessary.
// Returns methods validated in this call only. Returns either a full set of tags
// or nil for tags when tags are unchanged.
func addCreds(uid types.Uid, creds []MsgCredClient, extraTags []string,
	lang string, tmpToken []byte) ([]string, []string, error) {
	var validated []string
	for i := range creds {
		cr := &creds[i]
		vld := store.Store.GetValidator(cr.Method)
		if vld == nil {
			// Ignore unknown validator.
			continue
		}

		isNew, err := vld.Request(uid, cr.Value, lang, cr.Response, tmpToken)
		if err != nil {
			return nil, nil, err
		}

		if isNew && cr.Response != "" {
			// If response is provided and vld.Request did not return an error, the new request was
			// successfully validated.
			validated = append(validated, cr.Method)

			// Generate tags for these confirmed credentials.
			if globals.validators[cr.Method].addToTags {
				extraTags = append(extraTags, cr.Method+":"+cr.Value)
			}
		}
	}

	// Save tags potentially changed by the validator.
	if len(extraTags) > 0 {
		if utags, err := store.Users.UpdateTags(uid, extraTags, nil, nil); err == nil {
			extraTags = utags
		} else {
			logs.Warn.Println("add cred tags update failed:", err)
		}
	} else {
		extraTags = nil
	}
	return validated, extraTags, nil
}

// validatedCreds returns the list of validated credentials including those validated in this call.
// Returns all validated methods including those validated earlier and now.
// Returns either a full set of tags or nil for tags if tags are unchanged.
func validatedCreds(uid types.Uid, authLvl auth.Level, creds []MsgCredClient,
	errorOnFail bool) ([]string, []string, error) {
	// Check if credential validation is required.
	if len(globals.authValidators[authLvl]) == 0 {
		return nil, nil, nil
	}

	// Get all validated methods
	allCreds, err := store.Users.GetAllCreds(uid, "", true)
	if err != nil {
		return nil, nil, err
	}

	methods := make(map[string]struct{})
	for i := range allCreds {
		methods[allCreds[i].Method] = struct{}{}
	}

	// Add credentials which are validated in this call.
	// Unknown validators are removed.
	creds = normalizeCredentials(creds, false)
	var tagsToAdd []string
	for i := range creds {
		cr := &creds[i]
		if cr.Response == "" {
			// Ignore empty response.
			continue
		}

		vld := store.Store.GetValidator(cr.Method) // No need to check for nil, unknown methods are removed earlier.
		value, err := vld.Check(uid, cr.Response)
		if err != nil {
			// Check failed.
			if storeErr, ok := err.(types.StoreError); ok && storeErr == types.ErrCredentials {
				if errorOnFail {
					// Report invalid response.
					return nil, nil, types.ErrInvalidResponse
				}
				// Skip invalid response. Keep credential unvalidated.
				continue
			}
			// Actual error. Report back.
			return nil, nil, err
		}

		// Check did not return an error: the request was successfully validated.
		methods[cr.Method] = struct{}{}

		// Add validated credential to user's tags.
		if globals.validators[cr.Method].addToTags {
			tagsToAdd = append(tagsToAdd, cr.Method+":"+value)
		}
	}

	var tags []string
	if len(tagsToAdd) > 0 {
		// Save update to tags
		if utags, err := store.Users.UpdateTags(uid, tagsToAdd, nil, nil); err == nil {
			tags = utags
		} else {
			logs.Warn.Println("validated creds tags update failed:", err)
			tags = nil
		}
	} else {
		tags = nil
	}

	validated := make([]string, 0, len(methods))
	for method := range methods {
		validated = append(validated, method)
	}

	return validated, tags, nil
}

// deleteCred deletes user's credential.
// Returns full set of remaining tags or nil if tags are unchanged.
func deleteCred(uid types.Uid, authLvl auth.Level, cred *MsgCredClient) ([]string, error) {
	vld := store.Store.GetValidator(cred.Method)
	if vld == nil || cred.Value == "" {
		// Reject invalid request: unknown validation method or missing credential value.
		return nil, types.ErrMalformed
	}

	// Is this a required credential for this validation level?
	var isRequired bool
	for _, method := range globals.authValidators[authLvl] {
		if method == cred.Method {
			isRequired = true
			break
		}
	}

	// If credential is required, make sure the method remains validated even after this credential is deleted.
	if isRequired {
		// There could be multiple validated credentials for the same method thus we are getting a map with count
		// for each method.

		// Get all credentials of the given method.
		allCreds, err := store.Users.GetAllCreds(uid, cred.Method, false)
		if err != nil {
			return nil, err
		}

		// Check if it's OK to delete: there is another validated value
		// or this value is not validated in the first place.
		var okTodelete bool
		for _, cr := range allCreds {
			if (cr.Done && cr.Value != cred.Value) || (!cr.Done && cr.Value == cred.Value) {
				okTodelete = true
				break
			}
		}

		if !okTodelete {
			// Reject: this is the only validated credential and it must be provided.
			return nil, types.ErrPolicy
		}
	}

	// The credential is either not required or more than one credential is validated for the given method.
	err := vld.Remove(uid, cred.Value)
	if err != nil {
		if err == types.ErrNotFound {
			// Credential is not deleted because it's not found
			err = nil
		}
		return nil, err
	}

	// Remove generated tags for the deleted credential.
	var tags []string
	if globals.validators[cred.Method].addToTags {
		// This error should not be returned to user.
		if utags, err := store.Users.UpdateTags(uid, nil, []string{cred.Method + ":" + cred.Value}, nil); err == nil {
			tags = utags
		} else {
			logs.Warn.Println("delete cred: failed to update tags:", err)
			tags = nil
		}
	} else {
		tags = nil
	}

	return tags, nil
}

// Change user state: suspended/normal (ok).
// 1. Not needed -- Disable/enable logins (state checked after login).
// 2. If suspending, evict user's sessions. Skip this step if resuming.
// 3. Suspend/activate p2p with the user.
// 4. Suspend/activate grp topics where the user is the owner.
// 5. Update user's DB record.
func changeUserState(s *Session, uid types.Uid, user *types.User, msg *ClientComMessage) (bool, error) {
	state, err := types.NewObjState(msg.Acc.State)
	if err != nil || state == types.StateUndefined {
		logs.Warn.Println("replyUpdateUser: invalid account state", s.sid)
		return false, types.ErrMalformed
	}

	// State unchanged.
	if user.State == state {
		return false, nil
	}

	if state != types.StateOK {
		// Terminate all sessions.
		globals.sessionStore.EvictUser(uid, "")
	}

	err = store.Users.UpdateState(uid, state)
	if err != nil {
		return false, err
	}

	// Update state of all loaded in memory user's p2p & grp-owner topics.
	globals.hub.meta <- &metaReq{forUser: uid, state: state, sess: s}
	user.State = state

	return true, err
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
	var uid types.Uid

	if msg.Del.User == "" || msg.Del.User == s.uid.UserId() {
		// Delete current user.
		uid = s.uid
	} else if s.authLvl == auth.LevelRoot {
		// Delete another user.
		uid = types.ParseUserId(msg.Del.User)
		if uid.IsZero() {
			logs.Warn.Println("replyDelUser: invalid user ID", msg.Del.User, s.sid)
			s.queueOut(ErrMalformed(msg.Id, "", msg.Timestamp))
			return
		}
	} else {
		logs.Warn.Println("replyDelUser: illegal attempt to delete another user", msg.Del.User, s.sid)
		s.queueOut(ErrPermissionDenied(msg.Id, "", msg.Timestamp))
		return
	}

	// Disable all authenticators
	authnames := store.Store.GetAuthNames()
	for _, name := range authnames {
		hdl := store.Store.GetAuthHandler(name)
		if !hdl.IsInitialized() {
			continue
		}
		if err := hdl.DelRecords(uid); err != nil {
			// This could be completely benign, i.e. authenticator exists but not used.
			logs.Warn.Println("replyDelUser: failed to delete auth record", uid.UserId(), name, err, s.sid)
			if storeErr, ok := err.(types.StoreError); ok && storeErr == types.ErrUnsupported {
				// Authenticator refused to delete record: user account cannot be deleted.
				s.queueOut(ErrOperationNotAllowed(msg.Id, "", msg.Timestamp))
				return
			}
		}
	}

	// Terminate all sessions. Skip the current session so the requester gets a response.
	globals.sessionStore.EvictUser(uid, s.sid)
	// Remove user from cache and announce to cluster that the user is deleted.
	usersRemoveUser(uid)

	// Stop topics where the user is the owner and p2p topics.
	done := make(chan bool)
	globals.hub.unreg <- &topicUnreg{forUser: uid, del: msg.Del.Hard, done: done}
	<-done

	// Notify users of interest that the user is gone.
	if uoi, err := store.Users.GetSubs(uid); err == nil {
		presUsersOfInterestOffline(uid, uoi, "gone")
	} else {
		logs.Warn.Println("replyDelUser: failed to send notifications to users", err, s.sid)
	}

	// Notify subscribers of the group topics where the user was the owner that the topics were deleted.
	if ownTopics, err := store.Users.GetOwnTopics(uid); err == nil {
		for _, topicName := range ownTopics {
			if subs, err := store.Topics.GetSubs(topicName, nil); err == nil {
				presSubsOfflineOffline(topicName, types.TopicCatGrp, subs, "gone", &presParams{}, s.sid)
			} else {
				logs.Warn.Println("replyDelUser: failed to notify topic subscribers", err, topicName, s.sid)
			}
		}
	} else {
		logs.Warn.Println("replyDelUser: failed to send notifications to owned topics", err, s.sid)
	}

	// TODO: suspend all P2P topics with the user.

	// Delete user's records from the database.
	if err := store.Users.Delete(uid, msg.Del.Hard); err != nil {
		logs.Warn.Println("replyDelUser: failed to delete user", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.Timestamp, nil))
		return
	}

	s.queueOut(NoErr(msg.Id, "", msg.Timestamp))

	if s.uid == uid && s.multi == nil {
		// Evict the current session if it belongs to the deleted user.
		// No need to send it to multiplexing session: remote node will be notified separately.
		_, data := s.serialize(NoErrEvicted("", "", msg.Timestamp))
		s.stopSession(data)
	}
}

// Read user's state from DB.
func userGetState(uid types.Uid) (types.ObjState, error) {
	user, err := store.Users.Get(uid)
	if err != nil {
		return types.StateUndefined, err
	}
	if user == nil {
		return types.StateUndefined, types.ErrUserNotFound
	}
	return user.State, nil
}

// Subscribe or unsubscribe a single user's device to/from all FCM topics (channels).
func userChannelsSubUnsub(uid types.Uid, deviceID string, sub bool) {
	push.ChannelSub(&push.ChannelReq{
		Uid:      uid,
		DeviceID: deviceID,
		Unsub:    !sub,
	})
}

// UserCacheReq contains data which mutates one or more user cache entries.
type UserCacheReq struct {
	// Name of the node sending this request in case of cluster. Not set otherwise.
	Node string

	// UserId is set when count of unread messages is updated for a single user or
	// when the user is being deleted.
	UserId types.Uid
	// UserIdList  is set when subscription count is updated for users of a topic.
	UserIdList []types.Uid
	// Unread count (UserId is set)
	Unread int

	// In case of set UserId: treat Unread count as an increment as opposite to the final value.
	// In case of set UserIdList: intement (Inc == true) or decrement subscription count by one.
	Inc bool
	// User is being deleted, remove user from cache.
	Gone bool

	// Optional push notification
	PushRcpt *push.Receipt
}

type userCacheEntry struct {
	unread int
	topics int
}

// Preserved update entry kept while we read the unread counter from the DB.
type bufferedUpdate struct {
	val int
	inc bool
}

// Unread counter read result.
type ioResult struct {
	uid types.Uid
	val int
	err error
}

// Represents pending push notification receipt.
type pendingReceipt struct {
	// Number of unread counters currently being read from the DB.
	pendingIOs int
	// The index is needed by update and is maintained by the heap.Interface methods.
	index int
	// Underlying receipt.
	rcpt *push.Receipt
}

// Pending pushes organized as a priority queue (priority = number of pending IOs).
// It allows to quickly discover receipts ready for sending (num pending IOs is 0).
type pendingReceiptsQueue []*pendingReceipt

// Heap interface methods.
func (pq pendingReceiptsQueue) Len() int { return len(pq) }

func (pq pendingReceiptsQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].pendingIOs < pq[j].pendingIOs
}

func (pq pendingReceiptsQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *pendingReceiptsQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*pendingReceipt)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *pendingReceiptsQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

func (pq *pendingReceiptsQueue) fix(index int) {
	heap.Fix(pq, index)
}

// Initialize users cache.
func usersInit() {
	globals.usersUpdate = make(chan *UserCacheReq, 1024)

	go userUpdater()
}

// Shutdown users cache.
func usersShutdown() {
	if globals.usersUpdate != nil {
		globals.usersUpdate <- nil
	}
}

func usersUpdateUnread(uid types.Uid, val int, inc bool) {
	if globals.usersUpdate == nil || (val == 0 && inc) {
		return
	}

	upd := &UserCacheReq{UserId: uid, Unread: val, Inc: inc}
	if globals.cluster.isRemoteTopic(uid.UserId()) {
		// Send request to remote node which owns the user.
		globals.cluster.routeUserReq(upd)
	} else {
		select {
		case globals.usersUpdate <- upd:
		default:
		}
	}
}

// Process push notification.
func usersPush(rcpt *push.Receipt) {
	if globals.usersUpdate == nil {
		return
	}

	var local *UserCacheReq

	// In case of a cluster pushes will be initiated at the nodes which own the users.
	// Sort users into local and remote.
	if globals.cluster != nil {
		local = &UserCacheReq{PushRcpt: &push.Receipt{
			Payload: rcpt.Payload,
			Channel: rcpt.Channel,
			To:      make(map[types.Uid]push.Recipient),
		}}
		remote := &UserCacheReq{PushRcpt: &push.Receipt{
			Payload: rcpt.Payload,
			Channel: rcpt.Channel,
			To:      make(map[types.Uid]push.Recipient),
		}}

		for uid, recipient := range rcpt.To {
			if globals.cluster.isRemoteTopic(uid.UserId()) {
				remote.PushRcpt.To[uid] = recipient
			} else {
				local.PushRcpt.To[uid] = recipient
			}
		}

		if len(remote.PushRcpt.To) > 0 || remote.PushRcpt.Channel != "" {
			globals.cluster.routeUserReq(remote)
		}
	} else {
		local = &UserCacheReq{PushRcpt: rcpt}
	}

	if len(local.PushRcpt.To) > 0 || local.PushRcpt.Channel != "" {
		select {
		case globals.usersUpdate <- local:
		default:
		}
	}
}

// Start tracking a single user. Used for cache management.
// 'add' increments/decrements user's count of subscribed topics.
func usersRegisterUser(uid types.Uid, add bool) {
	if globals.usersUpdate == nil {
		return
	}

	upd := &UserCacheReq{UserIdList: make([]types.Uid, 1), Inc: add}
	upd.UserIdList[0] = uid

	if globals.cluster.isRemoteTopic(uid.UserId()) {
		// Send request to remote node which owns the user.
		globals.cluster.routeUserReq(upd)
	} else {
		select {
		case globals.usersUpdate <- upd:
		default:
		}
	}
}

// Stop tracking user and remove him from cache.
func usersRemoveUser(uid types.Uid) {
	if globals.usersUpdate == nil {
		return
	}

	upd := &UserCacheReq{UserId: uid, Gone: true}
	if !globals.cluster.isRemoteTopic(uid.UserId()) {
		select {
		case globals.usersUpdate <- upd:
		default:
		}
	}

	if globals.cluster != nil {
		// Announce to cluster even if the user is local.
		globals.cluster.routeUserReq(upd)
	}
}

// Account users as members of an active topic. Used for cache management.
// In case of a cluster this method is called only when the topic is local:
// globals.cluster.isRemoteTopic(t.name) == false
func usersRegisterTopic(t *Topic, add bool) {
	if globals.usersUpdate == nil {
		return
	}

	if t.cat == types.TopicCatFnd || t.cat == types.TopicCatMe {
		// Ignoring me and fnd topics.
		return
	}

	local := &UserCacheReq{Inc: add}

	// In case of a cluster UIDs could be local and remote. Process local UIDs locally,
	// send remote UIDs to other cluster nodes for processing. The UIDs may have to be
	// sent to multiple nodes.
	remote := &UserCacheReq{Inc: add}
	for uid, pud := range t.perUser {
		if pud.isChan {
			// Skip channel subscribers.
			continue
		}
		if globals.cluster.isRemoteTopic(uid.UserId()) {
			remote.UserIdList = append(remote.UserIdList, uid)
		} else {
			local.UserIdList = append(local.UserIdList, uid)
		}
	}

	if len(remote.UserIdList) > 0 {
		globals.cluster.routeUserReq(remote)
	}

	if len(local.UserIdList) > 0 {
		select {
		case globals.usersUpdate <- local:
		default:
			logs.Err.Println("User cache: globals.usersUpdate queue full: ", len(globals.usersUpdate))
		}
	}
}

// usersRequestFromCluster handles requests which came from other cluser nodes.
func usersRequestFromCluster(req *UserCacheReq) {
	if globals.usersUpdate == nil {
		return
	}

	select {
	case globals.usersUpdate <- req:
	default:
	}
}

// The go routine for processing updates to users cache.
func userUpdater() {
	// Caches unread counters and numbers of topics the user's subscribed to.
	usersCache := make(map[types.Uid]userCacheEntry)

	// Unread counter updates blocked by IO on per user basis. We flush them when the IO completes.
	perUserBuffers := make(map[types.Uid][]bufferedUpdate)

	// Push notification recipients blocked by IO (unread counters for some of the recipients
	// are being read from the database) on the per user basis.
	perUserPendingReceipts := make(map[types.Uid][]*pendingReceipt)

	// All pending push receipts organized as a priority queue by the number of pending IOs.
	receiptQueue := pendingReceiptsQueue{}

	// IO callback queue.
	ioDone := make(chan *ioResult, 1024)

	unreadUpdater := func(uid types.Uid, val int, inc bool) int {
		uce, ok := usersCache[uid]
		if !ok {
			logs.Err.Println("ERROR: attempt to update unread count for user who has not been loaded", uid)
			return unreadUpdateError
		}

		if uce.unread < 0 {
			// Unread counter not initialized yet. Maybe start a DB read?
			if updateBuf, ioInProgress := perUserBuffers[uid]; ioInProgress {
				// Buffer this update.
				updateBuf = append(updateBuf, bufferedUpdate{val: val, inc: inc})
				perUserBuffers[uid] = updateBuf
			} else {
				// Read the counter from DB.
				updateBuf = []bufferedUpdate{}
				perUserBuffers[uid] = updateBuf
				go func() {
					count, err := store.Users.GetUnreadCount(uid)
					if err != nil {
						logs.Warn.Println("users: failed to load unread count for user ", uid, ": ", err)
					}
					ioDone <- &ioResult{uid: uid, val: count, err: err}
				}()
			}
			return unreadUpdateIOPending

		} else if inc {
			uce.unread += val
		} else {
			uce.unread = val
		}

		usersCache[uid] = uce

		return uce.unread
	}

	for {
		select {
		case io := <-ioDone:
			// Unread counter read has completed.
			updateBuf, ok := perUserBuffers[io.uid]
			// Stop buffering updates. New updates will be handled normally.
			delete(perUserBuffers, io.uid)
			if io.err == nil {
				// Update counter.
				count := io.val
				if ok {
					for _, upd := range updateBuf {
						if upd.inc {
							count += upd.val
						} else {
							count = upd.val
						}
					}
				} else {
					logs.Warn.Println("ERROR: io didn't have an update buffer, uid ", io.uid)
				}
				if uce, ok := usersCache[io.uid]; ok {
					if uce.unread >= 0 {
						logs.Warn.Println("users: unread count double initialization, uid ", io.uid)
					}
					uce.unread = count
					usersCache[io.uid] = uce
				} else {
					logs.Warn.Println("users: missing users cache entry after IO completion, uid ", io.uid)
				}
			} else {
				logs.Err.Printf("users: io failed for uid[%s]: %s", io.uid, io.err)
			}
			// Now that the unread counter is initialized, handle pending push notification receipts.
			// Decrease pending IO counts in pending push receipts for this user.
			if pendingReceipts, ok := perUserPendingReceipts[io.uid]; ok {
				for _, pp := range pendingReceipts {
					pp.pendingIOs--
					receiptQueue.fix(pp.index)
				}
				delete(perUserPendingReceipts, io.uid)
			}
			// Send ready receipts.
			for receiptQueue.Len() > 0 && receiptQueue[0].pendingIOs == 0 {
				rcpt := heap.Pop(&receiptQueue).(*pendingReceipt).rcpt
				for uid, rcptTo := range rcpt.To {
					if uce, ok := usersCache[uid]; ok && uce.unread >= 0 {
						rcptTo.Unread = uce.unread
						rcpt.To[uid] = rcptTo
					}
				}
				push.Push(rcpt)
			}
		case upd := <-globals.usersUpdate:
			if globals.shuttingDown {
				// If shutdown is in progress we don't care to process anything.
				// ignore all calls.
				continue
			}

			// Shutdown requested.
			if upd == nil {
				globals.usersUpdate = nil
				// Dont' care to close the channel.
				goto Exit
			}

			// Request to send push notifications.
			if upd.PushRcpt != nil {
				// List of uids for which the unread count is being read from the DB.
				pendingUsers := []types.Uid{}
				for uid, rcptTo := range upd.PushRcpt.To {
					// Handle update
					unread := unreadUpdater(uid, 1, true)
					if unread >= 0 {
						rcptTo.Unread = unread
						upd.PushRcpt.To[uid] = rcptTo
					} else if unread == unreadUpdateIOPending {
						pendingUsers = append(pendingUsers, uid)
					}
				}
				if len(pendingUsers) == 0 {
					// All data present in memory. Just send the push.
					push.Push(upd.PushRcpt)
				} else {
					// We are waiting for IO. Add this receipt to the queues.
					pp := &pendingReceipt{
						pendingIOs: len(pendingUsers),
						rcpt:       upd.PushRcpt,
					}
					for _, uid := range pendingUsers {
						var queue []*pendingReceipt
						var ok bool
						if queue, ok = perUserPendingReceipts[uid]; !ok {
							queue = []*pendingReceipt{}
						}
						perUserPendingReceipts[uid] = append(queue, pp)
					}
					heap.Push(&receiptQueue, pp)
				}
				continue
			}

			// Request to add/remove user from cache.
			if len(upd.UserIdList) > 0 {
				for _, uid := range upd.UserIdList {
					uce, ok := usersCache[uid]
					if upd.Inc {
						if !ok {
							// This is a registration of a new user.
							// We are not loading unread count here, so set it to -1.
							uce.unread = -1
						}
						uce.topics++
						usersCache[uid] = uce
					} else if ok {
						if uce.topics > 1 {
							uce.topics--
							usersCache[uid] = uce
						} else {
							// Remove user from cache
							delete(usersCache, uid)
						}
					} else {
						// BUG!
						logs.Err.Println("ERROR: request to unregister user which has not been registered", uid)
					}
				}
				continue
			}

			if upd.Gone {
				// User is being deleted. Don't care if there is a record.
				delete(usersCache, upd.UserId)
				continue
			}

			// Request to update unread count.
			unreadUpdater(upd.UserId, upd.Unread, upd.Inc)
		}
	}

Exit:
	logs.Info.Println("users: shutdown")
}
