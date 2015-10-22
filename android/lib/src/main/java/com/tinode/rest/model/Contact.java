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
 *  File        :  Contact.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************/

package com.tinode.rest.model;

import java.util.ArrayList;
import java.util.Date;

/**
 * A representation of a contact in an address book
 */
public class Contact {
    public String id; // id of contact record
    public String contactId; // Id of the user
    public String parentId; // id of the user who created this contact
    public String name;
    public Date createdAt;
    public boolean active;
    public ArrayList<String> tags;
    public String comment;

    public Contact() {
        tags = new ArrayList<String>();
    }

    public String toString() {
        if (name != null) {
            return name;
        } else {
            return contactId.substring(0, 10) + "...";
        }
    }
}
