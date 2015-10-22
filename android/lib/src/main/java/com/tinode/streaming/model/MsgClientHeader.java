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
 *  File        :  MsgClientHeader.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *    Generic header of client to server messages
 *
 *****************************************************************************/

package com.tinode.streaming.model;

import java.util.Date;
import java.util.Map;

class MsgClientHeader {
    public String id;
    public String topic;
    public Map<String, Object> params;

    MsgClientHeader(String topic) {
        this.topic = topic;
    }

    public void addParam(String key, Object val) {
        params.put(key, val);
    }

    public boolean getBoolParam(String key) {
        return Utils.getBoolParam(params, key);
    }

    public String getStringParam(String key) {
        return Utils.getStringParam(params, key);
    }

    public Date getDateParam(String key) {
        return Utils.getDateParam(params, key);
    }

    public int getIntParam(String key) {
        return Utils.getIntParam(params, key);
    }

    public double getDoubleParam(String key) {
        return Utils.getDoubleParam(params, key);
    }

    public void setId(String id) {
        this.id = id;
    }
    public String getId() { return id; }

    public void setTopic(String topic) {
        this.topic = topic;
    }
    public String getTopic() {
        return topic;
    }
}

