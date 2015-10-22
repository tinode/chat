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
 *  File        :  PresTopic.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.streaming;

import android.util.Log;

import com.fasterxml.jackson.databind.type.TypeFactory;

import java.util.ArrayList;
import java.util.Set;

/**
 * Topic for receiving presence notifications
 */
public class PresTopic<T> extends Topic {
    private static final String TAG = "com.tinode.PresTopic";

    public PresTopic(Connection conn, Class<?> typeOfT, final PresListener<T> l) {
        super(conn, Connection.TOPIC_PRES, conn.getTypeFactory().
                        constructCollectionType(ArrayList.class, conn.getTypeFactory()
                                .constructParametricType(PresenceUpdate.class, typeOfT)),
                new Listener<ArrayList<PresenceUpdate<T>>>() {
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
                    }

                    @Override
                    public void onData(String from, ArrayList<PresenceUpdate<T>> content) {
                        for (PresenceUpdate<T> upd : content) {
                            l.onData(upd.who, upd.online, upd.status);
                        }
                    }
                }
        );
    }

    static class PresenceUpdate<T> {
        public String who;
        public Boolean online;
        public T status;

        public PresenceUpdate() {
        }
    }

    public interface PresListener<T> {
        public void onSubscribe(int code, String text);
        public void onUnsubscribe(int code, String text);
        public void onData(String who, Boolean online, T status);
    }
}
