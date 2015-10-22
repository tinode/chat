/*****************************************************************************
 *
 * Copyright 2014, Tinode, All Rights Reserved
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 *  File        :  Connection.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.streaming;

import android.os.Handler;
import android.support.v4.util.SimpleArrayMap;
import android.util.ArrayMap;
import android.util.Log;

import com.codebutler.android_websockets.WebSocketClient;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.core.JsonFactory;
import com.fasterxml.jackson.core.JsonParseException;
import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import com.fasterxml.jackson.databind.type.TypeFactory;
import com.tinode.Tinode;
import com.tinode.streaming.model.ClientMessage;
import com.tinode.streaming.model.MsgClientLogin;
import com.tinode.streaming.model.MsgClientPub;
import com.tinode.streaming.model.MsgClientSub;
import com.tinode.streaming.model.MsgClientUnsub;
import com.tinode.streaming.model.MsgServerCtrl;
import com.tinode.streaming.model.MsgServerData;
import com.tinode.streaming.model.ServerMessage;

import org.apache.http.message.BasicNameValuePair;

import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.Arrays;
import java.util.Map;
import java.util.Random;

/**
 * Singleton class representing a streaming communication channel between a client and a server
 *
 * First call {@link Tinode#initialize(java.net.URL, String)}, then call {@link #getInstance()}
 *
 * Created by gene on 2/12/14.
 */
public class Connection {
    private static final String TAG = "com.tinode.Connection";

    private static Connection sConnection;

    private int mPacketCount = 0;
    private int mMsgId = 0;

    private EventListener mListener;
    private Handler mHandler;
    private WebSocketClient mWsClient;

    // A list of outstanding requests, indexed by id
    protected SimpleArrayMap<String, Cmd> mRequests;

    // List of live subscriptions, key=[topic name], value=[topic, subscribed or
    // subscription pending]
    protected ArrayMap<String, Topic<?>> mSubscriptions;

    // Exponential backoff/reconnecting
    // TODO(gene): implement autoreconnect
    private boolean autoreconnect;
    private ExpBackoff backoff;

    public static final String TOPIC_NEW = "!new";
    public static final String TOPIC_ME = "!me";
    public static final String TOPIC_PRES = "!pres";
    public static final String TOPIC_P2P = "!usr:";

    protected Connection(URI endpoint, String apikey) {
        mRequests = new SimpleArrayMap<String, Cmd>();
        mSubscriptions = new ArrayMap<String, Topic<?>>();

        String path = endpoint.getPath();
        if (path.equals("")) {
            path = "/";
        } else if (path.lastIndexOf("/") != path.length() - 1) {
            path += "/";
        }
        path += "channels"; // http://www.example.com/v0/channels

        URI uri;
        try {
            uri = new URI(endpoint.getScheme(),
                    endpoint.getUserInfo(),
                    endpoint.getHost(),
                    endpoint.getPort(),
                    path,
                    endpoint.getQuery(),
                    endpoint.getFragment());
        } catch (URISyntaxException e) {
            e.printStackTrace();
            return;
        }

        mWsClient = new WebSocketClient(uri, new WebSocketClient.Listener() {
            @Override
            public void onConnect() {
                Log.d(TAG, "Websocket connected!");
                if (mHandler != null) {
                    mHandler.post(new Runnable() {
                        @Override
                        public void run() {
                            onWsConnect();
                        }
                    });
                } else {
                    onWsConnect();
                }
            }

            @Override
            public void onMessage(final String message) {
                Log.d(TAG, message);
                mPacketCount ++;
                if (mHandler != null) {
                    mHandler.post(new Runnable() {
                        @Override
                        public void run() {
                            onWsMessage(message);
                        }
                    });
                } else {
                    onWsMessage(message);
                }
            }

            @Override
            public void onMessage(byte[] data) {
                // do nothing, server does not send binary frames
                Log.i(TAG, "binary message received (should not happen)");
            }

            @Override
            public void onDisconnect(final int code, final String reason) {
                Log.d(TAG, "Disconnected :(");
                // Reset packet counter
                mPacketCount = 0;
                if (mHandler != null) {
                    mHandler.post(new Runnable() {
                        @Override
                        public void run() {
                            onWsDisconnect(code, reason);
                        }
                    });
                } else {
                    onWsDisconnect(code, reason);
                }

                if (autoreconnect) {
                    // TODO(gene): add autoreconnect
                }
            }

            @Override
            public void onError(final Exception error) {
                Log.i(TAG, "Connection error", error);
                if (mHandler != null) {
                    mHandler.post(new Runnable() {
                        @Override
                        public void run() {
                            onWsError(error);
                        }
                    });
                } else {
                    onWsError(error);
                }
            }
        }, Arrays.asList(new BasicNameValuePair("X-Tinode-APIKey", apikey)));
    }

    protected void onWsConnect() {
        if (backoff != null) {
            backoff.reset();
        }
    }

    protected void onWsMessage(String message) {
        ServerMessage pkt = parseServerMessageFromJson(message);
        if (pkt == null) {
            Log.i(TAG, "Failed to parse packet");
            return;
        }

        boolean dispatchDone = false;
        if (pkt.ctrl != null) {
            if (mPacketCount == 1) {
                // The first packet from a fresh connection
                if (mListener != null) {
                    mListener.onConnect(pkt.ctrl.code, pkt.ctrl.text, pkt.ctrl.getParams());
                    dispatchDone = true;
                }
            }
        } else if (pkt.data == null) {
            Log.i(TAG, "Empty packet received");
        }

        if (!dispatchDone) {
            // Dispatch message to topics
            if (mListener != null) {
                dispatchDone = mListener.onMessage(pkt);
            }

            if (!dispatchDone) {
                dispatchPacket(pkt);
            }
        }
    }

    protected void onWsDisconnect(int code, String reason) {
        // Dump all records of pending requests, responses won't be coming anyway
        mRequests.clear();
        // Inform topics that they were disconnected, clear list of subscribe topics
        disconnectTopics();
        mSubscriptions.clear();

        Tinode.clearMyId();

        if (mListener != null) {
            mListener.onDisconnect(code, reason);
        }
    }

    protected void onWsError(Exception err) {
        // do nothing
    }

    /**
     * Listener for connection-level events. Don't override onMessage for default behavior.
     *
     * By default all methods are called on the websocket thread. If you want to change that
     * set handler with {@link #setHandler(android.os.Handler)}
     */
    public void setListener(EventListener l) {
        mListener = l;
    }
    public EventListener getListener() {
        return mListener;
    }

    /**
     * By default all {@link Connection.EventListener} methods are called on websocket thread.
     * If you want to change that and, for instance, have them called on the main application thread, do something
     * like this: {@code mConnection.setHandler(new Handler(Looper.getMainLooper()));}
     *
     * @param h handler to use
     * @see android.os.Handler
     * @see android.os.Looper
     */
    public void setHandler(Handler h) {
        mHandler = h;
    }

    /**
     * Get an instance of Connection if it already exists or create a new instance.
     *
     * @return Connection instance
     */
    public static Connection getInstance() {
        if (sConnection == null) {
            sConnection = new Connection(Tinode.getEndpointUri(), Tinode.getApiKey());
        }
        return sConnection;
    }

    /**
     * Establish a connection with the server. It opens a websocket in a separate
     * thread. Success or failure will be reported through callback set by
     * {@link #setListener(Connection.EventListener)}.
     *
     * This is a non-blocking call.
     *
     * @param autoreconnect not implemented yet
     * @return true if a new attempt to open a connection was performed, false if connection already exists
     */
    public boolean Connect(boolean autoreconnect) {
        // TODO(gene): implement autoreconnect
        this.autoreconnect = autoreconnect;

        if (!mWsClient.isConnected()) {
            mWsClient.connect();
            return true;
        }

        return false;
    }

    /**
     * Gracefully close websocket connection
     *
     * @return true if an actual attempt to disconnect was made, false if there was no connection already
     */
    public boolean Disconnect() {
        if (mWsClient.isConnected()) {
            mWsClient.disconnect();
            return true;
        }

        return false;
    }

    /**
     * Send a basic login packet to the server. A connection must be established prior to calling
     * this method. Success or failure will be reported through {@link Connection.EventListener#onLogin(int, String)}
     *
     *  @param uname user name
     *  @param password password
     *  @return id of the message (which is either "login" or null)
     *  @throws NotConnectedException if there is no connection
     */
    public String Login(String uname, String password) throws NotConnectedException {
        return login(MsgClientLogin.LOGIN_BASIC, MsgClientLogin.makeToken(uname, password));
    }

    /**
     * Send a token login packet to server. A connection must be established prior to calling
     * this method. Success or failure will be reported through {@link Connection.EventListener#onLogin(int, String)}
     *
     *  @param token a previously obtained or generated login token
     *  @return id of the message (which is either "login" or null)
     *  @throws NotConnectedException if there is not connection
     */
    public String Login(String token) throws NotConnectedException {
        return login(MsgClientLogin.LOGIN_TOKEN, token);
    }

    protected String login(String scheme, String secret) throws NotConnectedException {
        ClientMessage msg = new ClientMessage();
        msg.login = new MsgClientLogin();
        msg.login.setId("login");
        msg.login.Login(scheme, secret);
        try {
            send(Tinode.getJsonMapper().writeValueAsString(msg));
            expectReply(Cmd.LOGIN, "login", null);
            return "login";
        } catch (JsonProcessingException e) {
            return null;
        }
    }

    /**
     * Execute subscription request for a topic.
     *
     * Users should call {@link Topic#Subscribe()} instead
     *
     * @param topic to subscribe
     * @return request id
     * @throws NotConnectedException
     */
    protected String subscribe(Topic<?> topic) throws NotConnectedException {
        wantAkn(true);  // Message dispatching to Topic<?> requires acknowledgements

        String name = topic.getName();
        if (name == null || name.equals("")) {
            Log.i(TAG, "Empty topic name");
            return null;
        }
        String id = Subscribe(name);
        if (id != null) {
            expectReply(Cmd.SUB, id, topic);
        }
        return id;
    }

    /**
     * Low-level subscription request. The subsequent messages on this topic will not
     * be automatically dispatched. A {@link Topic#Subscribe()} should be normally used instead.
     *
     * @param topicName name of the topic to subscribe to
     * @return id of the sent subscription packet, if {@link #wantAkn(boolean)} is set to true, null otherwise
     * @throws NotConnectedException
     */
    public String Subscribe(String topicName) throws NotConnectedException {
        ClientMessage msg = new ClientMessage();
        msg.sub = new MsgClientSub(topicName);
        String id = getNextId();
        msg.sub.setId(id);
        try {
            send(Tinode.getJsonMapper().writeValueAsString(msg));
            return id;
        } catch (JsonProcessingException e) {
            Log.i(TAG, "Failed to serialize message", e);
            return null;
        }
    }

    protected String unsubscribe(Topic<?> topic)  throws NotConnectedException {
        wantAkn(true);
        String id = Unsubscribe(topic.getName());
        if (id != null) {
            expectReply(Cmd.UNSUB, id, topic);
        }
        return id;
    }

    /**
     * Low-level request to unsubscribe topic. A {@link com.tinode.streaming.Topic#Unsubscribe()} should be normally
     * used instead.
     *
     * @param topicName name of the topic to subscribe to
     * @return id of the sent subscription packet, if {@link #wantAkn(boolean)} is set to true, null otherwise
     * @throws NotConnectedException
     */
    public String Unsubscribe(String topicName) throws NotConnectedException {
        ClientMessage msg = new ClientMessage();
        msg.unsub = new MsgClientUnsub(topicName);
        msg.unsub.setId(getNextId());
        mSubscriptions.remove(topicName);
        try {
            send(Tinode.getJsonMapper().writeValueAsString(msg));
            return msg.unsub.getId();
        } catch (JsonProcessingException e) {
            return null;
        }
    }

    protected String publish(Topic<?> topic, Object content) throws NotConnectedException {
        wantAkn(true);
        String id = Publish(topic.getName(), content);
        if (id != null) {
            expectReply(Cmd.PUB, id, topic);
        }
        return id;
    }

    /**
     * Low-level request to publish data. A {@link Topic#Publish(Object)} should be normally
     * used instead.
     *
     * @param topicName name of the topic to publish to
     * @param data payload to publish to topic
     * @return id of the sent packet, if {@link #wantAkn(boolean)} is set to true, null otherwise
     * @throws NotConnectedException
     */
    public String Publish(String topicName, Object data) throws NotConnectedException {
        ClientMessage msg = new ClientMessage();
        msg.pub = new MsgClientPub<Object>(topicName, data);
        msg.pub.setId(getNextId());
        try {
            send(Tinode.getJsonMapper().writeValueAsString(msg));
            return msg.pub.getId();
        } catch (JsonProcessingException e) {
            return null;
        }
    }

    /**
     * Assigns packet id, if needed, converts {@link com.tinode.streaming.model.ClientMessage} to Json string,
     * then calls {@link #send(String)}
     *
     * @param msg message to send
     * @return id of the packet (could be null)
     * @throws NotConnectedException
     */
    protected String sendPacket(ClientMessage<?> msg) throws NotConnectedException {
        String id = getNextId();

        if (id !=null) {
            if (msg.pub != null) {
                msg.pub.setId(id);
            } else if (msg.sub != null) {
                msg.sub.setId(id);
            } else if (msg.unsub != null) {
                msg.unsub.setId(id);
            } else if (msg.login != null) {
                msg.login.setId(id);
            }
        }

        try {
            send(Tinode.getJsonMapper().writeValueAsString(msg));
            return id;
        } catch (JsonProcessingException e) {
            return null;
        }
    }

    /**
     * Writes a string to websocket.
     *
     * @param data string to write to websocket
     * @throws NotConnectedException
     */
    protected void send(String data) throws NotConnectedException {
        if (mWsClient.isConnected()) {
            mWsClient.send(data);
        } else {
            throw new NotConnectedException("Send called without a live connection");
        }
    }

    /**
     * Request server to send acknowledgement packets. Server responds with such packets if
     * client includes non-empty id fiel0d into outgoing packets.
     * If set to true, {@link #getNextId()} will return a string representation of a random integer between
     * 16777215 and 33554430
     *
     * @param akn true to request akn packets, false otherwise
     * @return previous value
     */
    public boolean wantAkn(boolean akn) {
        boolean prev = (mMsgId != 0);
        if (akn) {
            mMsgId = 0xFFFFFF + (int) (Math.random() * 0xFFFFFF);
        } else {
            mMsgId = 0;
        }
        return prev;
    }

    /**
     * Makes a record of an outgoing packet. This is used to match requests to replies.
     *
     * @param type packet type, see constants in {@link Cmd}
     * @param id packet id
     * @param topic topic (could be null)
     */
    protected void expectReply(int type, String id, Topic<?> topic) {
        mRequests.put(id, new Cmd(type, id, topic));
    }

    /**
     * Check if there is a live connection.
     *
     * @return true if underlying websocket is connected
     */
    public boolean isConnected() {
        return mWsClient.isConnected();
    }

    /**
     * Get token returned by the server after successful authentication.
     *
     * @return security token
     */
    public String getAuthToken() {
        return Tinode.getAuthToken();
    }

    /**
     * Get ID of the currently authenticated user.
     *
     * @return user ID
     */
    public String getMyUID() {
        return Tinode.getMyId();
    }

    /**
     * Check if connection has been authenticated
     *
     * @return true, if connection was authenticated (Login was successfully called), false otherwise
     */
    public boolean isAuthenticated() {
        return Tinode.isAuthenticated();
    }

    /**
     * Obtain a subscribed !me topic ({@link MeTopic}).
     *
     * @return subscribed !me topic or null if !me is not subscribed
     */
    public MeTopic<?> getSubscribedMeTopic() {
        return (MeTopic) mSubscriptions.get(TOPIC_ME);
    }

    /**
     * Obtain a subscribed !pres topic {@link PresTopic}.
     *
     * @return subscribed !pres topic or null if !pres is not subscribed
     */
    public PresTopic<?> getSubscribedPresTopic() {
        return (PresTopic) mSubscriptions.get(TOPIC_PRES);
    }

    /**
     * Obtain a subscribed topic by name
     *
     * @param name name of the topic to find
     * @return subscribed topic or null if no such topic was found
     */
    public Topic<?> getSubscribedTopic(String name) {
        return mSubscriptions.get(name);
    }

    /**
     * Returns {@link com.fasterxml.jackson.databind.type.TypeFactory} which can be used to define complex types.
     * This is needed only if payload in {@link MsgClientPub} is complex, such as {@link java.util.Collection} or other
     * generic class.
     *
     * @return TypeFactory
     */
    public TypeFactory getTypeFactory() {
        return Tinode.getTypeFactory();
    }

    /**
     * Finds topic for the packet and calls topic's {@link Topic#dispatch(ServerMessage)} method.
     * This method can be safely called from the UI thread after overriding
     * {@link Connection.EventListener#onMessage(ServerMessage)}
     *
     * There are two types of messages:
     * <ul>
     * <li>Control packets in response to requests sent by this client</li>
     * <li>Data packets</li>
     * </ul>
     *
     * This method dispatches control packets by matching id of the message with a map of
     * outstanding requests.<p/>
     * The data packets are dispatched by topic name. If topic is unknown,
     * an onNewTopic is fired, then message is dispatched to the topic it returns.
     *
     * @param pkt packet to be dispatched
     * @return true if packet was successfully dispatched, false if topic was not found
     */
    @SuppressWarnings("unchecked")
    public boolean dispatchPacket(ServerMessage<?> pkt) {
        Log.d(TAG, "dispatchPacket: processing message");
        if (pkt.ctrl != null) {
            Log.d(TAG, "dispatchPacket: control");
            // This is a response to previous action
            String id = pkt.getId();
            if (id != null) {
                Cmd cmd = mRequests.remove(id);
                if (cmd != null) {
                    switch (cmd.type) {
                        case Cmd.LOGIN:
                            if (pkt.ctrl.code == 200) {
                                Tinode.setAuthParams(pkt.ctrl.getStringParam("uid"),
                                        pkt.ctrl.getStringParam("token"),
                                        pkt.ctrl.getDateParam("expires"));
                            }
                            if (mListener != null) {
                                mListener.onLogin(pkt.ctrl.code, pkt.ctrl.text);
                            }
                            return true;
                        case Cmd.SUB:
                            if (pkt.ctrl.code >= 200 && pkt.ctrl.code < 300) {
                                if (TOPIC_NEW.equals(cmd.source.getName())) {
                                    // Replace "!new" with the actual topic name
                                    cmd.source.setName(pkt.getTopic());
                                }
                                mSubscriptions.put(cmd.source.getName(), cmd.source);
                                Log.d(TAG, "Sub completed: " + cmd.source.getName());
                            }
                            return cmd.source.dispatch(pkt);
                        case Cmd.UNSUB:
                            // This could fail for two reasons:
                            // 1. Not subscribed
                            // 2. Something else
                            // Either way, no need to remove topic from subscription in case of
                            // failure
                            if (pkt.ctrl.code >= 200 && pkt.ctrl.code < 300) {
                                mSubscriptions.remove(cmd.source.getName());
                            }
                            return cmd.source.dispatch(pkt);
                        case Cmd.PUB:
                            return cmd.source.dispatch(pkt);
                    }
                }
            } else {
                Log.i(TAG, "Unexpected control packet");
            }
        } else if (pkt.data != null) {
            // This is a new data packet
            String topicName = pkt.getTopic();
            Log.d(TAG, "dispatchPacket: data for " + topicName);
            if (topicName != null) {
                // The topic must be in the list of subscriptions already.
                // It must have been created in {@link #pareseMsgServerData} or earlier
                Topic topic = mSubscriptions.get(topicName);
                if (topic != null) {
                    // This generates the "unchecked" warning
                    return topic.dispatch(pkt);
                } else {
                    Log.i(TAG, "Packet for unknown topic " + topicName);
                }
            }
        }
        return false;
    }

    protected void registerP2PTopic(MeTopic<?> topic) {
        mSubscriptions.put(topic.getName(), topic);
    }

    /**
     * Enumerate subscribed topics and inform each one that it was disconnected.
     */
    private void disconnectTopics() {
        for (Map.Entry<String, Topic<?>> e : mSubscriptions.entrySet()) {
            e.getValue().disconnected();
        }
    }

    /**
     * Parse JSON received from the server into {@link ServerMessage}
     *
     * @param jsonMessage
     * @return ServerMessage or null
     */
    @SuppressWarnings("unchecked")
    protected ServerMessage<?> parseServerMessageFromJson(String jsonMessage) {
        MsgServerCtrl ctrl = null;
        MsgServerData<?> data = null;
        try {
            ObjectMapper mapper = Tinode.getJsonMapper();
            JsonParser parser = mapper.getFactory().createParser(jsonMessage);
            // Sanity check: verify that we got "Json Object":
            if (parser.nextToken() != JsonToken.START_OBJECT) {
                throw new JsonParseException("Packet must start with an object",
                        parser.getCurrentLocation());
            }
            // Iterate over object fields:
            while (parser.nextToken() != JsonToken.END_OBJECT) {
                String name = parser.getCurrentName();
                parser.nextToken();
                if (name.equals("ctrl")) {
                    ctrl = mapper.readValue(parser, MsgServerCtrl.class);
                } else if (name.equals("data")) {
                    data = parseMsgServerData(parser);
                } else { // Unrecognized field, ignore
                    Log.i(TAG, "Unknown field in packet: '" + name +"'");
                }
            }
            parser.close(); // important to close both parser and underlying reader
        } catch (JsonParseException e) {
            e.printStackTrace();
        } catch (IOException e) {
            e.printStackTrace();
        }

        if (ctrl != null) {
            return new ServerMessage(ctrl);
        } else if (data != null) {
            // This generates the "unchecked" warning
            return new ServerMessage(data);
        }
        return null;
    }

    protected MsgServerData<?> parseMsgServerData(JsonParser parser) throws JsonParseException,
            IOException {
        ObjectMapper mapper = Tinode.getJsonMapper();
        JsonNode data = mapper.readTree(parser);
        if (data.has("topic")) {
            String topicName = data.get("topic").asText();
            Topic<?> topic = getSubscribedTopic(topicName);
            // Is this a topic we are subscribed to?
            if (topic == null) {
                // This is a new topic

                // Try to find a topic pending subscription by packet id
                if (data.has("id")) {
                    String id = data.get("id").asText();
                    Cmd cmd = mRequests.get(id);
                    if (cmd != null) {
                        topic = cmd.source;
                    }
                }

                // If topic was not found among pending subscriptions, try to create it
                if (topic == null && mListener != null) {
                    topic = mListener.onNewTopic(topicName);
                    if (topic != null) {
                        topic.setStatus(Topic.STATUS_SUBSCRIBED);
                        mSubscriptions.put(topicName, topic);
                    } else if (topicName.startsWith(TOPIC_P2P)) {
                        // Client refused to create topic. If this is a P2P topic, assume
                        // the payload is the same as "!me"
                        topic = getSubscribedMeTopic();
                    }
                }
            }

            JavaType typeOfData;
            if (topic == null) {
                Log.i(TAG, "Data message for unknown topic [" + topicName + "]");
                typeOfData = mapper.getTypeFactory().constructType(Object.class);
            } else {
                typeOfData = topic.getDataType();
            }
            MsgServerData packet = new MsgServerData();
            if (data.has("id")) {
                packet.id = data.get("id").asText();
            }
            packet.topic = topicName;
            if (data.has("origin")) {
                packet.origin = data.get("origin").asText();
            }
            if (data.has("content")) {
                packet.content = mapper.readValue(data.get("content").traverse(), typeOfData);
            }
            return packet;
        } else {
            throw new JsonParseException("Invalid data packet: missing topic name",
                    parser.getCurrentLocation());
        }
    }

    /**
     * Get a string representation of a random number, to be used as a packet id.
     *
     * @return reasonably unique id
     * @see #wantAkn(boolean)
     */
    synchronized private String getNextId() {
        if (mMsgId == 0) {
            return null;
        }
        return String.valueOf(++mMsgId);
    }

    static class Cmd {
        static final int LOGIN = 1;
        static final int SUB = 2;
        static final int UNSUB = 3;
        static final int PUB = 4;

        int type;
        String id;
        Topic<?> source;

        Cmd(int type, String id, Topic<?> src) {
            this.type = type;
            this.id = id;
            this.source = src;
        }
        Cmd(int type, String id) {
            this(type, id, null);
        }
    }

    /**
     * Callback interface called by Connection when it receives events from the websocket.
     *
     */
    public static abstract class EventListener {
        /**
         * Connection was established successfully
         *
         * @param code should be always 201
         * @param reason should be always "Created"
         * @param params server parameters, such as protocol version
         */
        public abstract void onConnect(int code, String reason, Map<String, Object> params);

        /**
         * Connection was dropped
         *
         * @param code numeric code of the error which caused connection to drop
         * @param reason error message
         */
        public abstract void onDisconnect(int code, String reason);

        /**
         * Result of successful or unsuccessful {@link #Login(String)} attempt.
         *
         * @param code a numeric value between 200 and 2999 on success, 400 or higher on failure
         * @param text "OK" on success or error message
         */
        public abstract void onLogin(int code, String text);

        /**
         * A request to create a new topic. This is usually called when someone initiates a P2P
         * conversation. No need to call Subscribe on this topic.
         *
         * @param topicName name of the new topic to create
         * @return newly created topic or null
         */
        public abstract Topic<?> onNewTopic(String topicName);

        /**
         * Handle server message. Default handler calls {@code #dispatchPacket(...)} on a
         * websocket thread.
         * A subclassed listener may wish to call {@code dispatchPacket()} on a UI thread
         *
         * @param msg message to be processed
         * @return true if no further processing is needed, for instance if you called
         * {@link #dispatchPacket(ServerMessage)}, false if Connection should process the message.
         * @see #dispatchPacket(com.tinode.streaming.model.ServerMessage)
         */
        public boolean onMessage(ServerMessage<?> msg) {
            return false;
        }
    }

    /**
     * TODO(gene): implement autoreconnect with exponential backoff
     */
    class ExpBackoff {
        private int mRetryCount = 0;
        final private long SLEEP_TIME_MILLIS = 500; // 500 ms
        final private long MAX_DELAY = 1800000; // 30 min
        private Random random = new Random();

        void reset() {
            mRetryCount = 0;
        }

        /**
         *
         * @return
         */
        long getSleepTimeMillis() {
            int attempt = mRetryCount;
            return Math.min(SLEEP_TIME_MILLIS * (random.nextInt(1 << attempt) + (1 << (attempt+1))),
                    MAX_DELAY);
        }
    }
}
