#!/bin/bash

# Credential extractor. Tino the Chatbot is created with a random password. The password is written 
# to stdout by tinode-db. The script converts it to chatbot's authentication cookie.

# The script takes a string like 'usr;tino;usrImlot_X9vAc;cOuTvzVa' (ignored;login;user_id;password)
# and formats it into a json chatbot's authentication cookie like 
# '{"schema": "basic", "secret": "username:password", "user": "user_id"}'. 
while read line; do
  IFS=';' read -r -a parts <<< "$line"
  echo "{\"schema\": \"basic\", \"secret\": \"${parts[1]}:${parts[3]}\", \"user\": \"${parts[2]}\"}"
done < /dev/stdin
