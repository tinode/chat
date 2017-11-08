#!/bin/bash

# COnverting a string like 'usr;tino;usrImlot_X9vAc;cOuTvzVa' (ignored;login;user_id;password)
# into a json like '{"schema": "basic", "secret": "username:password", "user": "user_id"}'
while read line; do
  IFS=';' read -r -a parts <<< "$line"
  echo "{\"schema\": \"basic\", \"secret\": \"${parts[1]}:${parts[3]}\", \"user\": \"${parts[2]}\"}"
done < /dev/stdin
