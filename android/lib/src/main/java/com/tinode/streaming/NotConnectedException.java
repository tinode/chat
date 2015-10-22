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
 *  File        :  NotConnectedException.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.streaming;

import java.io.IOException;

/**
 * Created by gene on 2/25/14.
 */
public final class NotConnectedException extends IOException {
    public NotConnectedException(String msg) {
        super(msg);
    }

    public NotConnectedException(String msg, Throwable cause) {
        super(msg, cause);
    }

    /**
     * Creates exception with the specified cause. Consider using
     * {@link #NotConnectedException(String, Throwable)} instead if you can
     * describe what actually happened.
     *
     * @param cause root exception that caused this exception to be thrown.
     */
    public NotConnectedException(Throwable cause) {
        super(cause);
    }
}
