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
 *  File        :  Request.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.rest;

import android.util.Log;

import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.type.TypeFactory;
import com.tinode.Tinode;

import java.net.MalformedURLException;
import java.net.URL;
import java.util.ArrayList;

/**
 * REST request constructor
 */
public class Request {
    private static final String TAG = "com.tinode.rest.Request";

    protected String mMethod;
    protected Object mPayload;
    protected URL mUrl;
    protected JavaType mBaseType;

    protected boolean mIsList;

    protected Request(URL endpoint) {
        mUrl = endpoint;
        mIsList = true;
    }

    public static Request build() {
        return new Request(Tinode.getEndpointUrl());
    }

    public Request setEndpoint(URL endpoint) {
        mUrl = endpoint;
        mIsList = true;
        return this;
    }

    public Request setMethod(String method) {
        mMethod = method;
        return this;
    }

    public Request addKind(String kind) {
        try {
            String path = mUrl.getPath();
            if (path.equals("")) {
                path = "/";
            } else if (path.lastIndexOf('/') != (path.length() - 1)) {
                // Ensure path is terminated by slash
                path = path + "/";
            }
            mUrl = new URL(mUrl, path + kind);
            mIsList = true;
        } catch (MalformedURLException e) {
            Log.i(TAG, e.toString());
        }
        return this;
    }

    public  Request addObjectId(String id) {
        try {
            mUrl = new URL(mUrl, mUrl.getPath() + "/:" + id);
            mIsList = false;
        } catch (MalformedURLException e) {
            Log.i(TAG, e.toString());
        }
        return this;
    }

    public String getMethod() {
        return mMethod;
    }

    public URL getUrl() {
        return mUrl;
    }

    public Object getPayload() {
        return mPayload;
    }

    public Request setPayload(Object payload) {
        mPayload = payload;
        return this;
    }

    public Request setInDataType(JavaType type) {
        mBaseType = type;
        return this;
    }

    public Request setInDataType(Class<?> type) {
        mBaseType = Tinode.getTypeFactory().constructType(type);
        return this;
    }

    public JavaType getInDataType() {
        TypeFactory tf = Tinode.getTypeFactory();
        if (mBaseType == null) {
            mBaseType = tf.constructType(Object.class);
        }

        if (mIsList) {
            return tf.constructCollectionType(ArrayList.class, mBaseType);
        } else {
            return mBaseType;
        }
    }


}
