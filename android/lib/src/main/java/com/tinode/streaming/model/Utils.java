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
 *  File        :  Utils.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 * 
 *    Utility class. Currently supports setters/getters of message parameters.
 *  
 *****************************************************************************/
package com.tinode.streaming.model;

import android.util.Log;

import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Map;

public class Utils {
    final private static String TAG = "com.tinode.streaming.model.Utils";

    public static boolean getBoolParam(Map<String, Object> params, String key) {
        if (params.containsKey(key)) {
            Object val = params.get(key);
            if (val instanceof Boolean) {
                return (Boolean) val;
            } else if (val instanceof Integer) {
                return ((Integer) val != 0);
            }
        }

        return false;
    }

    public static String getStringParam(Map<String, Object> params, String key) {
        if (params.containsKey(key)) {
            Object val = params.get(key);
            if (val instanceof String) {
                return (String) val;
            }
        }

        return null;
    }

    public static Date getDateParam(Map<String, Object> params, String key) {
        if (params.containsKey(key)) {
            Object val = params.get(key);
            if (val instanceof Date) {
                return (Date) val;
            } else if (val instanceof String) {
                String str = (String) val;
                Date d = null;
                if (str.endsWith("Z")) {
                    // Parsing time in RFC3339 format in UTC time zone with no fraction
                    try {
                        SimpleDateFormat sdf = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'");
                        d = sdf.parse(str);
                    }
                    catch (java.text.ParseException e) {
                        Log.d(TAG, "Invalid or unrecognized date format");
                    }
                    return d;
                }
            }
        }

        return null;
    }

    public static int getIntParam(Map<String, Object> params, String key) {
        if (params.containsKey(key)) {
            Object val = params.get(key);
            if (val instanceof Integer) {
                return (Integer) val;
            } else if (val instanceof Double) {
                return ((Double) val).intValue();
            }
        }
        return 0;
    }

    public static double getDoubleParam(Map<String, Object> params, String key) {
        if (params.containsKey(key)) {
            Object val = params.get(key);
            if (val instanceof Double) {
                return (Double) val;
            } else if (val instanceof Integer) {
                return ((Integer) val).doubleValue();
            }
        }
        return 0.0;
    }
}
