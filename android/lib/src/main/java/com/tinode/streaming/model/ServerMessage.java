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
 *  File        :  ServerMessage.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *   Convenience wrapper for Server to Client messages, data messages are of 
 *   type T
 *
 *****************************************************************************/
package com.tinode.streaming.model;

public class ServerMessage<T> {
    public MsgServerCtrl ctrl;
    public MsgServerData<T> data;

    public ServerMessage() {
    }

    public ServerMessage(MsgServerCtrl ctrl) {
        this.ctrl = ctrl;
    }

    public ServerMessage(MsgServerData<T> data) {
        this.data = data;
    }

    public String getTopic() {
        if (ctrl != null) {
            return ctrl.topic;
        } else if (data != null) {
            return data.topic;
        }
        return null;
    }

    public String getId() {
        if (ctrl != null) {
            return ctrl.id;
        } else if (data != null) {
            return data.id;
        }
        return null;
    }

    @Override
    public String toString() {
        if (ctrl != null) {
            return "ctrl:{" + ctrl.toString() + "}";
        } else if (data != null) {
            return "pub:{" + data.toString() + "}";
        }
        return "null";
    }
}
