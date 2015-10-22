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
 *  File        :  
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *  Root object for communication with the server
 *
 *****************************************************************************/

package com.tinode;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.core.JsonFactory;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.type.TypeFactory;

import java.net.URI;
import java.net.URISyntaxException;
import java.net.URL;
import java.util.Date;

public final class Tinode {
    private static String sApiKey;
    private static URL sServerUrl;

    private static String sAuthToken;
    private static Date sAuthExpires;

    private static String sMyId;

    private static ObjectMapper sJsonMapper;
    private static JsonFactory sJsonFactory;
    private static TypeFactory sTypeFactory;

    private Tinode() {
    }

    /**
     * Initialize Tinode package
     *
     * @param url debuggin only. will be hardcoded and removed from here
     * @param key api key provided by Tinode
     */
    public static void initialize(URL url, String key) {
        sJsonMapper = new ObjectMapper();
        // Silently ignore unknown properties:
        sJsonMapper.disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES);
        // Skip null fields from serialization:
        sJsonMapper.setSerializationInclusion(JsonInclude.Include.NON_NULL);
        sTypeFactory = sJsonMapper.getTypeFactory();

        sApiKey = key;
        sServerUrl = url;
    }

    public static String getApiKey() {
        return sApiKey;
    }

    public static URL getEndpointUrl() {
        return sServerUrl;
    }
    public static URI getEndpointUri() {
        try {
            return sServerUrl.toURI();
        } catch (URISyntaxException e) {
            return null;
        }
    }

    synchronized public static void setAuthParams(String myId, String token, Date expires) {
        sMyId = myId;
        sAuthToken = token;
        sAuthExpires = expires;
    }

    synchronized public static void clearAuthParams() {
        sMyId = null;
        sAuthToken = null;
        sAuthExpires = null;
    }

    synchronized public static String getAuthToken() {
        if (sAuthToken != null) {
            Date now = new Date();
            if (!sAuthExpires.before(now)) {
                return sAuthToken;
            } else {
                sAuthToken = null;
                sAuthExpires = null;
            }
        }
        return null;
    }

    public static String getMyId() {
        return sMyId;
    }

    public static void clearMyId() {
        sMyId = null;
    }

    public static boolean isAuthenticated() {
        return (sMyId != null);
    }

    public static TypeFactory getTypeFactory() {
        return sTypeFactory;
    }

    public static ObjectMapper getJsonMapper() {
        return sJsonMapper;
    }
}
