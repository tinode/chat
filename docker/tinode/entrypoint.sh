#!/bin/bash

# Remove the old config.
rm -f working.config

# If EXT_CONFIG is set, use it as a config file.
if [ ! -z "$EXT_CONFIG" ] ; then
	CONFIG="$EXT_CONFIG"

	# Enable push notifications.
	if [ ! -z "$FCM_SENDER_ID" ] ; then
		FCM_PUSH_ENABLED=true
	fi

else
	CONFIG=working.config

	# Enable email verification if $SMTP_SERVER is defined.
	if [ ! -z "$SMTP_SERVER" ] ; then
		EMAIL_VERIFICATION_REQUIRED='"auth"'
	fi

	# Enable TLS (httpS).
	if [ ! -z "$TLS_DOMAIN_NAME" ] ; then
		TLS_ENABLED=true
	fi

	# Enable push notifications.
	if [ ! -z "$FCM_CRED_FILE" ] ; then
		FCM_PUSH_ENABLED=true
	fi

	# Generate a new 'working.config' from template and environment
	while IFS='' read -r line || [[ -n $line ]] ; do
	    while [[ "$line" =~ (\$[A-Z_][A-Z_0-9]*) ]] ; do
	        LHS=${BASH_REMATCH[1]}
	        RHS="$(eval echo "\"$LHS\"")"
	        line=${line//$LHS/$RHS}
	    done
	    echo "$line" >> working.config
	done < config.template
fi

# If push notifications are enabled, generate client-side firebase config file.
if [ ! -z "$FCM_PUSH_ENABLED" ] ; then
	# Write client config to static/firebase-init.js
	echo "const FIREBASE_INIT={messagingSenderId: \"$FCM_SENDER_ID\", messagingVapidKey: \"$FCM_VAPID_KEY\"};"$'\n' > static/firebase-init.js
else
	# Create an empty firebase-init.js
	echo "" > static/firebase-init.js
fi

# Initialize the database if it has not been initialized yet or if data reset has been requested.
./init-db --reset=${RESET_DB} --config=${CONFIG} --data=data.json | grep "usr;tino;" > /botdata/tino-password

if [ -s /botdata/tino-password ] ; then
	# Convert Tino's authentication credentials into a cookie file.
	# The cookie file is also used to check if database has been initialized.

	# /botdata/tino-password could be empty if DB was not updated. In such a case the
	# /botdata/.tn-cookie will not be modified.
	./credentials.sh /botdata/.tn-cookie < /botdata/tino-password
fi

# Run the tinode server.
./tinode --config=${CONFIG} --static_data=static 2> /var/log/tinode.log
