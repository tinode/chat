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
 *  File        :  MeTopic.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.streaming;

import com.fasterxml.jackson.databind.JavaType;
import com.tinode.streaming.model.ClientMessage;
import com.tinode.streaming.model.MsgClientPub;

/**
 * Topic for announcing one's presence and exchanging P2P messages
 */
public class MeTopic<T> extends Topic<T> {
    protected MeTopic(final Connection conn, String party, JavaType typeOfT, final MeListener<T> l) {
        super(conn, party, typeOfT, new Listener<T>() {
            @Override
            public void onSubscribe(int code, String text) {
                l.onSubscribe(code, text);
            }

            @Override
            public void onUnsubscribe(int code, String text) {
                l.onUnsubscribe(code, text);
            }

            @Override
            public void onPublish(String topic, int code, String text) {
                l.onPublish(topic, code, text);
            }

            @Override
            public void onData(String from, T content) {
                l.onData(!from.equals(conn.getMyUID()), content);
            }
        });
    }

    public MeTopic(Connection conn, Class<?> typeOfT, MeListener<T> l) {
        this(conn, Connection.TOPIC_ME, conn.getTypeFactory().constructType(typeOfT), l);
    }
    /**
     * Send a message to a specific user without creating a topic dedicated to the conversation.
     * Normally one should create a dedicated topic for a p2p chat
     *
     * @param to user ID to send message to
     * @param content message payload
     * @throws NotConnectedException
     */
    public void Publish(String to, T content) throws NotConnectedException {
        ClientMessage msg = new ClientMessage();
        String topic = Connection.TOPIC_P2P + to;
        msg.pub = new MsgClientPub<T>(topic, content);
        String id = mConnection.sendPacket(msg);
        mReplyExpected.put(id, PACKET_TYPE_PUB);
    }

    public interface MeListener<T> {
        public void onSubscribe(int code, String text);
        public void onUnsubscribe(int code, String text);
        public void onPublish(String party, int code, String text);
        public void onData(boolean isReply, T content);
    }

    public static String topicNameForContact(String contactId) {
        return Connection.TOPIC_P2P + contactId;
    }

    public MeTopic<T> startP2P(String party, MeListener<T> l) {
        if (!party.startsWith(Connection.TOPIC_P2P)) {
            party = topicNameForContact(party);
        }
        MeTopic<T> topic = new MeTopic<T>(mConnection, party, mTypeOfData, l);
        mConnection.registerP2PTopic(topic);

        return topic;
    }

    public <U> MeTopic<U> startP2P(String party, Class<?> typeOfU, MeListener<U> l) {
        if (!party.startsWith(Connection.TOPIC_P2P)) {
            party = Connection.TOPIC_P2P + party;
        }
        MeTopic<U> topic = new MeTopic<U>(mConnection, party, mConnection.getTypeFactory().constructType(typeOfU), l);
        mConnection.registerP2PTopic(topic);
        return topic;
    }
}
