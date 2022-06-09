/******************************************************************************
 *
 *  Description :
 *    Video call handling (establishment, metadata exhange and termination).
 *
 *****************************************************************************/
package main

import (
	"strconv"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store/types"
)

// Video call constants.
const (
	// Events for call between users A and B.
	//
	// Call started (A is dialing B).
	constCallEventInvite = "invite"
	// B has received the call but hasn't picked it up yet.
	constCallEventRinging = "ringing"
	// B has accepted the call.
	constCallEventAccept = "accept"
	// WebRTC SDP & ICE data exchange events.
	constCallEventOffer        = "offer"
	constCallEventAnswer       = "answer"
	constCallEventIceCandidate = "ice-candidate"
	// Call finished by either side or server.
	constCallEventHangUp = "hang-up"

	// Message headers representing call states.
	// Call is established.
	constCallMsgAccepted = "accepted"
	// Previously establied call has successfully finished.
	constCallMsgFinished = "finished"
	// Call is dropped (e.g. because of an error).
	constCallMsgDisconnected = "disconnected"
	// Call is missed (the callee didn't pick up the phone).
	constCallMsgMissed = "missed"
	// Call is declined (the callee hung up before picking up).
	constCallMsgDeclined = "declined"
)

// videoCall describes video call that's being established or in progress.
type videoCall struct {
	// Call participants.
	parties map[*Session]callPartyData
	// Call message seq ID.
	seq int
	// Call message content.
	content interface{}
	// Call message content mime type.
	contentMime interface{}
	// Time when the call was accepted.
	acceptedAt time.Time
}

func (call *videoCall) messageHead(newState string, duration int) map[string]interface{} {
	head := map[string]interface{}{
		"replace": ":" + strconv.Itoa(call.seq),
		"webrtc":  newState,
	}
	if duration > 0 {
		head["webrtc-duration"] = duration
	}
	if call.contentMime != nil {
		head["mime"] = call.contentMime
	}
	return head
}

// Generates server info message template for the video call event.
func (call *videoCall) infoMessage(event string) *ServerComMessage {
	return &ServerComMessage{
		Info: &MsgServerInfo{
			What:  "call",
			Event: event,
			SeqId: call.seq,
		},
	}
}

// Returns Uid and session of the present video call originator
// if a call is being established or in progress.
func (t *Topic) getCallOriginator() (types.Uid, *Session) {
	if t.currentCall == nil {
		return types.ZeroUid, nil
	}
	for sess, p := range t.currentCall.parties {
		if p.isOriginator {
			return p.uid, sess
		}
	}
	return types.ZeroUid, nil
}

// Handles video call invite (initiation)
// (in response to msg = {pub head=[mime: application/x-tiniode-webrtc]}).
func (t *Topic) handleCallInvite(msg *ClientComMessage, asUid types.Uid) {
	if t.currentCall != nil {
		// There's already another call in progress.
		msg.sess.queueOut(ErrCallBusyReply(msg, types.TimeNow()))
		return
	}
	if t.cat != types.TopicCatP2P {
		msg.sess.queueOut(ErrPermissionDeniedReply(msg, types.TimeNow()))
		return
	}

	tgt := t.p2pOtherUser(asUid)
	t.infoCallSubsOffline(msg.AsUser, tgt, constCallEventInvite, t.lastID, nil, msg.sess.sid, false)
	// Call being establshed.
	t.currentCall = &videoCall{
		parties:     make(map[*Session]callPartyData),
		seq:         t.lastID,
		content:     msg.Pub.Content,
		contentMime: msg.Pub.Head["mime"],
		acceptedAt:  time.Now(),
	}
	t.currentCall.parties[msg.sess] = callPartyData{
		uid:          asUid,
		isOriginator: true,
	}
	// Wait for constCallEstablishmentTimeout for the other side to accept the call.
	t.callEstablishmentTimer.Reset(time.Duration(globals.callEstablishmentTimeout) * time.Second)
}

// Handles events on existing video call (acceptance, termination, metadata exchange).
// (in response to msg = {note what=call}).
func (t *Topic) handleCallEvent(msg *ClientComMessage) {
	if t.currentCall == nil {
		// Must initiate call first.
		return
	}
	if t.isInactive() {
		// Topic is paused or being deleted.
		return
	}

	call := msg.Note
	if t.currentCall.seq != call.SeqId {
		// Call not found.
		return
	}

	asUid := types.ParseUserId(msg.AsUser)

	_, userFound := t.perUser[asUid]
	if !userFound {
		// User not found in topic.
		return
	}

	switch call.Event {
	case constCallEventRinging, constCallEventAccept:
		// Invariants:
		// 1. Call has been initiated but not been established yet.
		if len(t.currentCall.parties) != 1 {
			return
		}
		originatorUid, originator := t.getCallOriginator()
		if originator == nil {
			logs.Warn.Printf("topic[%s]: video call (seq %d) has no originator. Terminating.", t.name, t.currentCall.seq)
			t.terminateCallInProgress(false)
			return
		}
		// 2. These events may only arrive from the callee.
		if originator == msg.sess || originatorUid == asUid {
			return
		}
		// Prepare a {info} message to forward to the call originator.
		forwardMsg := t.currentCall.infoMessage(call.Event)
		forwardMsg.Info.From = msg.AsUser
		forwardMsg.Info.Topic = t.original(originatorUid)
		if call.Event == constCallEventAccept {
			// The call has been accepted.
			// Send a replacement {data} message to the topic.
			replaceWith := constCallMsgAccepted
			head := t.currentCall.messageHead(replaceWith, 0)
			msgCopy := *msg
			msgCopy.AsUser = originatorUid.UserId()
			if err := t.saveAndBroadcastMessage(&msgCopy, originatorUid, false, nil,
				head, t.currentCall.content); err != nil {
				return
			}
			// Add callee data to t.currentCall.
			t.currentCall.parties[msg.sess] = callPartyData{
				uid:          asUid,
				isOriginator: false,
			}

			// Notify other clients that the call has been accepted.
			t.infoCallSubsOffline(msg.AsUser, asUid, call.Event, t.lastID, call.Payload, msg.sess.sid, false)
			t.callEstablishmentTimer.Stop()
		}
		originator.queueOut(forwardMsg)
	case constCallEventOffer, constCallEventAnswer, constCallEventIceCandidate:
		// Call metadata exchange. Either side of the call may send these events.
		// Simply forward them to the other session.
		var otherUid types.Uid
		var otherEnd *Session
		for sess, p := range t.currentCall.parties {
			if sess != msg.sess {
				otherUid = p.uid
				otherEnd = sess
				break
			}
		}
		if otherEnd == nil {
			return
		}
		// All is good. Send {info} message to the otherEnd.
		forwardMsg := t.currentCall.infoMessage(call.Event)
		forwardMsg.Info.From = msg.AsUser
		forwardMsg.Info.Topic = t.original(otherUid)
		forwardMsg.Info.Payload = call.Payload
		otherEnd.queueOut(forwardMsg)
	case constCallEventHangUp:
		t.maybeEndCallInProgress(msg.AsUser, msg, false)
	default:
		logs.Warn.Printf("topic[%s]: video call (seq %d) received unexpected call event: %s", t.name, t.currentCall.seq, call.Event)
	}
}

// Ends current call in response to a client hangup request (msg).
func (t *Topic) maybeEndCallInProgress(from string, msg *ClientComMessage, callDidTimeout bool) {
	if t.currentCall == nil {
		return
	}
	t.callEstablishmentTimer.Stop()
	originator, _ := t.getCallOriginator()
	var replaceWith string
	var callDuration int64
	if from != "" && len(t.currentCall.parties) == 2 {
		// This is a call in progress.
		replaceWith = constCallMsgFinished
		callDuration = time.Now().Sub(t.currentCall.acceptedAt).Milliseconds()
	} else {
		if from != "" {
			// User originated hang-up.
			if from == originator.UserId() {
				// Originator/caller requested event.
				replaceWith = constCallMsgMissed
			} else {
				// Callee requested event.
				replaceWith = constCallMsgDeclined
			}
		} else {
			// Server initiated disconnect.
			// Call hasn't been established. Just drop it.
			if callDidTimeout {
				replaceWith = constCallMsgMissed
			} else {
				replaceWith = constCallMsgDisconnected
			}
		}
	}

	// Send a message indicating the call has ended.
	head := t.currentCall.messageHead(replaceWith, int(callDuration))
	msgCopy := *msg
	msgCopy.AsUser = originator.UserId()
	if err := t.saveAndBroadcastMessage(&msgCopy, originator, false, nil, head, t.currentCall.content); err != nil {
		logs.Err.Printf("topic[%s]: failed to write finalizing message for call seq id %d - '%s'", t.name, t.currentCall.seq, err)
	}

	// Send {info} hangup event to the subscribed sessions.
	resp := t.currentCall.infoMessage(constCallEventHangUp)
	t.broadcastToSessions(resp)

	// Let all other sessions know the call is over.
	for tgt := range t.perUser {
		t.infoCallSubsOffline(from, tgt, constCallEventHangUp, t.currentCall.seq, nil, "", true)
	}
	t.currentCall = nil
}

// Server initiated call termination.
func (t *Topic) terminateCallInProgress(callDidTimeout bool) {
	if t.currentCall == nil {
		return
	}
	uid, sess := t.getCallOriginator()
	if sess == nil || uid.IsZero() {
		// Just drop the call.
		logs.Warn.Printf("topic[%s]: video call (seq %d) has no originator. Terminating.", t.name, t.currentCall.seq)
		t.currentCall = nil
		return
	}
	// Dummy hangup request.
	dummy := &ClientComMessage{
		Original:  t.original(uid),
		RcptTo:    uid.UserId(),
		AsUser:    uid.UserId(),
		Timestamp: types.TimeNow(),
		sess:      sess,
	}

	t.maybeEndCallInProgress("", dummy, callDidTimeout)
}
