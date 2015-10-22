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
 *  File        :  MsgServerCtrl.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *   Server to client control message 
 *
 *****************************************************************************/

package com.tinode.streaming.model;

import java.util.Date;
import java.util.Map;

public class MsgServerCtrl {
    public String id;
    public int code;
    public String text;
    public String topic;
    public Map<String, Object> params;

    @Override
    public String toString() {
        StringBuffer buff = new StringBuffer();
        if (id != null && !id.isEmpty()) {
            buff.append("(id:").append(id).append(")");
        }
        buff.append(code).append(" ").append(text);
        if (params != null && params.size() > 0) {
            buff.append(" [");
            String sep = "";
            for (Map.Entry<String, Object> p : params.entrySet()) {
                buff.append(sep);
                sep = ",";
                buff.append(p.getKey()).append(":");
                StringBuffer val = new StringBuffer(p.getValue().toString());
                if (val.length() > 6) {
                    buff.append(val, 0, 5).append("...");
                } else {
                    buff.append(val);
                }
            }
            buff.append("]");
        }
        return buff.toString();
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

    public Map<String, Object> getParams() {
        return params;
    }
}
