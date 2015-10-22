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
 *  File        :  MsgClientLogin.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *    Client login packet
 *
 *****************************************************************************/

package com.tinode.streaming.model;

public class MsgClientLogin {
    public static final String LOGIN_BASIC = "basic";
    public static final String LOGIN_TOKEN = "token";

    public String id;
    public String scheme; // "basic" or "token"
    public String secret; // <uname + ":" + password> or <HMAC-signed token>
    public String expireIn; // String "5000s" for 5000 seconds or "128h26m" for 128 hours 26 min

    public MsgClientLogin() {
        expireIn = "24h";
    }

    public MsgClientLogin(String scheme, String secret, String expireIn) {
        this.scheme = scheme;
        this.secret = secret;
        this.expireIn = expireIn;
    }

    public MsgClientLogin(String scheme, String secret) {
        this(scheme, secret, "24h");
    }

    public void setId(String id) {
        this.id = id;
    }

    public void setExpireIn(String expireIn) {
        this.expireIn = expireIn;
    }

    public static String makeToken(String uname, String password) {
        return uname + ":" + password;
    }

    public void Login(String scheme, String secret) {
        this.scheme = scheme;
        this.secret = secret;
    }

    public void basicLogin(String uname, String password) {
        Login(LOGIN_BASIC, makeToken(uname, password));
    }

    public void tokenLogin(String token) {
        Login(LOGIN_TOKEN, token);
    }

}
