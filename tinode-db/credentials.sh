#!/bin/bash

# Credential extractor. Tino the Chatbot is created with a random password. The password is written
# to stdout by tinode-db. The script converts it to chatbot's authentication cookie.

# The script takes a string like 'usr;tino;usrImlot_X9vAc;cOuTvzVa' (ignored;login;user_id;password)
# and formats it into a json chatbot's authentication cookie like
# '{"schema": "basic", "secret": "username:password", "user": "user_id"}'.

COOKIE_FILE=$@

while read line; do
  IFS=';' read -r -a parts <<< "$line"
  if [ ${#parts[@]} -eq 0 ] ; then
    continue
  fi

  # If the name of the cookie file is given, write to file
  # Otherwise write to stdout
  if [ "$COOKIE_FILE" ]; then
    exec 3>"$COOKIE_FILE"
  else
    exec 3>&1
  fi

  echo "{\"schema\": \"basic\", \"secret\": \"${parts[1]}:${parts[3]}\", \"user\": \"${parts[2]}\"}" 1>&3
  break
done < /dev/stdin
