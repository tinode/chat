/******************************************************************************
 *  Description :
 *    Topic in a cluster which serves as a local representation of the master
 *    topic hosted at another node.
 *****************************************************************************/

package main

import (
	"net/http"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store/types"
)

func (t *Topic) runProxy(hub *Hub) {
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	for {
		select {
		case join := <-t.reg:
			// Request to add a connection to this topic
			if t.isInactive() {
				join.sess.queueOut(ErrLockedReply(join, types.TimeNow()))
			} else if err := globals.cluster.routeToTopicMaster(ProxyReqJoin, join, t.name, join.sess); err != nil {
				// Response (ctrl message) will be handled when it's received via the proxy channel.
				logs.Warn.Println("proxy topic: route join request from proxy to master failed:", err)
			}
			if join.sess.inflightReqs != nil {
				join.sess.inflightReqs.Done()
			}

		case msg := <-t.unreg:
			if !t.handleProxyLeaveRequest(msg, killTimer) {
				logs.Warn.Println("Failed to update proxy topic state for leave request", msg.sess.sid)
			}
			if msg.init && msg.sess.inflightReqs != nil {
				// If it's a client initiated request.
				msg.sess.inflightReqs.Done()
			}

		case msg := <-t.clientMsg:
			// Content message intended for broadcasting to recipients
			if err := globals.cluster.routeToTopicMaster(ProxyReqBroadcast, msg, t.name, msg.sess); err != nil {
				logs.Warn.Println("proxy topic: route broadcast request from proxy to master failed:", err)
			}

		case msg := <-t.serverMsg:
			// FIXME: should something be done here?
			logs.Err.Printf("ERROR!!! Unexpected server-side message in proxy topic %#v", msg)

		case msg := <-t.meta:
			// Request to get/set topic metadata
			if err := globals.cluster.routeToTopicMaster(ProxyReqMeta, msg, t.name, msg.sess); err != nil {
				logs.Warn.Println("proxy topic: route meta request from proxy to master failed:", err)
			}

		case upd := <-t.supd:
			// Either an update to 'me' user agent from one of the sessions or
			// background session comes to foreground.
			req := ProxyReqMeUserAgent
			tmpSess := &Session{userAgent: upd.userAgent}
			if upd.sess != nil {
				// Subscribed user may not match session user. Find out who is subscribed
				pssd, ok := t.sessions[upd.sess]
				if !ok {
					logs.Warn.Println("proxy topic: sess update request from detached session")
					continue
				}
				req = ProxyReqBgSession
				tmpSess.uid = pssd.uid
				tmpSess.sid = upd.sess.sid
				tmpSess.userAgent = upd.sess.userAgent
			}
			if err := globals.cluster.routeToTopicMaster(req, nil, t.name, tmpSess); err != nil {
				logs.Warn.Println("proxy topic: route sess update request from proxy to master failed:", err)
			}

		case msg := <-t.proxy:
			t.proxyMasterResponse(msg, killTimer)

		case sd := <-t.exit:
			// Tell sessions to remove the topic
			for s := range t.sessions {
				s.detachSession(t.name)
			}

			if err := globals.cluster.topicProxyGone(t.name); err != nil {
				logs.Warn.Printf("topic proxy shutdown [%s]: failed to notify master - %s", t.name, err)
			}

			// Report completion back to sender, if 'done' is not nil.
			if sd.done != nil {
				sd.done <- true
			}
			return

		case <-killTimer.C:
			// Topic timeout
			hub.unreg <- &topicUnreg{rcptTo: t.name}
		}
	}
}

// Takes a session leave request, forwards it to the topic master and
// modifies the local state accordingly.
// Returns whether the operation was successful.
func (t *Topic) handleProxyLeaveRequest(msg *ClientComMessage, killTimer *time.Timer) bool {
	// Detach session from topic; session may continue to function.
	var asUid types.Uid
	if msg.init {
		asUid = types.ParseUserId(msg.AsUser)
	}

	if asUid.IsZero() {
		if pssd, ok := t.sessions[msg.sess]; ok {
			asUid = pssd.uid
		} else {
			logs.Warn.Println("proxy topic: leave request sent for unknown session")
			return false
		}
	}
	// Remove the session from the topic without waiting for a response from the master node
	// because by the time the response arrives this session may be already gone from the session store
	// and we won't be able to find and remove it by its sid.
	_, result := t.remSession(msg.sess, asUid)
	if !msg.init {
		// Explicitly specify the uid because the master multiplex session needs to know which
		// of its multiple hosted sessions to delete.
		msg.AsUser = asUid.UserId()
		msg.Leave = &MsgClientLeave{}
		msg.init = true
	}

	if err := globals.cluster.routeToTopicMaster(ProxyReqLeave, msg, t.name, msg.sess); err != nil {
		logs.Warn.Println("proxy topic: route leave request from proxy to master failed:", err)
	}
	if len(t.sessions) == 0 {
		// No more sessions attached. Start the countdown.
		killTimer.Reset(idleProxyTopicTimeout)
	}
	return result
}

// proxyMasterResponse at proxy topic processes a master topic response to an earlier request.
func (t *Topic) proxyMasterResponse(msg *ClusterResp, killTimer *time.Timer) {
	// Kills topic after a period of inactivity.
	keepAlive := idleProxyTopicTimeout

	if msg.SrvMsg.Pres != nil && msg.SrvMsg.Pres.What == "acs" && msg.SrvMsg.Pres.Acs != nil {
		// If the server changed acs on this topic, update the internal state.
		t.updateAcsFromPresMsg(msg.SrvMsg.Pres)
	}

	if msg.OrigSid == "*" {
		// It is a broadcast.
		switch {
		case msg.SrvMsg.Pres != nil || msg.SrvMsg.Data != nil || msg.SrvMsg.Info != nil:
			// Regular broadcast.
			t.handleProxyBroadcast(msg.SrvMsg)
		case msg.SrvMsg.Ctrl != nil:
			// Ctrl broadcast. E.g. for user eviction.
			t.proxyCtrlBroadcast(msg.SrvMsg)
		default:
		}
	} else {
		sess := globals.sessionStore.Get(msg.OrigSid)
		if sess == nil {
			logs.Warn.Println("topic_proxy: session not found; already terminated?", msg.OrigSid)
		}
		switch msg.OrigReqType {
		case ProxyReqJoin:
			if sess != nil && msg.SrvMsg.Ctrl != nil {
				// TODO: do we need to let the master topic know that the subscription is not longer valid
				// or is it already informed by the session when it terminated?

				// Subscription result.
				if msg.SrvMsg.Ctrl.Code < 300 {
					sess.sessionStoreLock.Lock()
					// Make sure the session isn't gone yet.
					if session := globals.sessionStore.Get(msg.OrigSid); session != nil {
						// Successful subscriptions.
						t.addSession(session, msg.SrvMsg.uid, types.IsChannel(msg.SrvMsg.Ctrl.Topic))
						session.addSub(t.name, &Subscription{
							broadcast: t.clientMsg,
							done:      t.unreg,
							meta:      t.meta,
							supd:      t.supd,
						})
					}
					sess.sessionStoreLock.Unlock()

					killTimer.Stop()
				} else if len(t.sessions) == 0 {
					killTimer.Reset(keepAlive)
				}
			}
		case ProxyReqBroadcast, ProxyReqMeta:
			// no processing
		case ProxyReqLeave:
			if msg.SrvMsg != nil && msg.SrvMsg.Ctrl != nil {
				if msg.SrvMsg.Ctrl.Code < 300 {
					if sess != nil {
						t.remSession(sess, sess.uid)
					}
				}
				// All sessions are gone. Start the kill timer.
				if len(t.sessions) == 0 {
					killTimer.Reset(keepAlive)
				}
			}

		default:
			logs.Err.Printf("proxy topic [%s] received response referencing unexpected request type %d",
				t.name, msg.OrigReqType)
		}

		if sess != nil && !sess.queueOut(msg.SrvMsg) {
			logs.Err.Println("topic proxy: timeout")
		}
	}
}

// handleProxyBroadcast broadcasts a Data, Info or Pres message to sessions attached to this proxy topic.
func (t *Topic) handleProxyBroadcast(msg *ServerComMessage) {
	if t.isInactive() {
		// Ignore broadcast - topic is paused or being deleted.
		return
	}

	if msg.Data != nil {
		t.lastID = msg.Data.SeqId
	}

	t.broadcastToSessions(msg)
}

// proxyCtrlBroadcast broadcasts a ctrl command to certain sessions attached to this proxy topic.
func (t *Topic) proxyCtrlBroadcast(msg *ServerComMessage) {
	if msg.Ctrl.Code == http.StatusResetContent && msg.Ctrl.Text == "evicted" {
		// We received a ctrl command for evicting a user.
		if msg.uid.IsZero() {
			logs.Err.Panicf("topic[%s]: proxy received evict message with empty uid", t.name)
		}
		for sess := range t.sessions {
			// Proxy topic may only have ordinary sessions. No multiplexing or proxy sessions here.
			if _, removed := t.remSession(sess, msg.uid); removed {
				sess.detachSession(t.name)
				if sess.sid != msg.SkipSid {
					sess.queueOut(msg)
				}
			}
		}
	}
}

// updateAcsFromPresMsg modifies user acs in Topic's perUser struct based on the data in `pres`.
func (t *Topic) updateAcsFromPresMsg(pres *MsgServerPres) {
	uid := types.ParseUserId(pres.Src)
	dacs := pres.Acs
	if uid.IsZero() {
		logs.Warn.Printf("proxy topic[%s]: received acs change for invalid user id '%s'", t.name, pres.Src)
		return
	}

	// If t.perUser[uid] does not exist, pud is initialized with blanks, otherwise it gets existing values.
	pud := t.perUser[uid]
	if err := pud.modeWant.ApplyMutation(dacs.Want); err != nil {
		logs.Warn.Printf("proxy topic[%s]: could not process acs change - want: %+v", t.name, err)
		return
	}
	if err := pud.modeGiven.ApplyMutation(dacs.Given); err != nil {
		logs.Warn.Printf("proxy topic[%s]: could not process acs change - given: %+v", t.name, err)
		return
	}
	// Update existing or add new.
	t.perUser[uid] = pud
}
