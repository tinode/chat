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
 *  File        :  Rest.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/
package com.tinode.rest;

import android.os.AsyncTask;
import android.util.Log;

import com.fasterxml.jackson.core.JsonParseException;
import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.JsonMappingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tinode.Tinode;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.net.HttpURLConnection;
import java.net.MalformedURLException;
import java.net.URL;
import java.util.ArrayList;
import java.util.Collection;


/**
 * Execute REST request, return response
 */
public class Rest {
    private static final String TAG = "com.tinode.rest.Rest";

    public static final String METHOD_GET = "GET";
    public static final String METHOD_POST = "POST";
    public static final String METHOD_PUT = "PUT";
    public static final String METHOD_DELETE = "DELETE";

    protected Rest() {
    }

    public static Request buildRequest() {
        return Request.build();
    }

    public static <T> T executeBlocking(Request req) {
        URL url = req.getUrl();
        Log.d(TAG, "Requesting " + url.toString());
        ObjectMapper mapper = Tinode.getJsonMapper();

        try {
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            try {
                conn.setRequestMethod(req.getMethod());
                Object payload = req.getPayload();
                conn.setDoOutput(payload != null);
                conn.setRequestProperty("Content-Type", "application/json");
                conn.setRequestProperty("Accept", "application/json");
                conn.setRequestProperty("X-Tinode-APIKey", Tinode.getApiKey());
                conn.setRequestProperty("X-Tinode-Token", Tinode.getAuthToken());

                if (payload != null) {
                    OutputStreamWriter out = new OutputStreamWriter(conn.getOutputStream(), "UTF-8");
                    mapper.writeValue(out, payload);
                    out.flush();
                    out.close();
                    Log.d(TAG, "Sent: " + payload.toString());
                }

                int code = conn.getResponseCode();
                BufferedReader bin = new BufferedReader(new InputStreamReader(conn.getInputStream(), "UTF-8"));
                T val = mapper.readValue(bin, req.getInDataType());
                bin.close();

                return val;

            } catch (MalformedURLException e) {
                Log.i(TAG, e.toString());
            } finally {
                conn.disconnect();
            }
        } catch (IOException e) {
            Log.i(TAG, e.toString());
        }
        return null;
    }
}
