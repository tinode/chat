#!/bin/bash

# Remove the old config.
rm -f working.config

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

	# Write client config to static/firebase-init.js
	echo "const FIREBASE_INIT={messagingSenderId: \"$FCM_SENDER_ID\", messagingVapidKey: \"$FCM_VAPID_KEY\"};"$'\n' > static/firebase-init.js
else
	# Create an empty firebase-init.js
	echo "" > static/firebase-init.js
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


# Initialize the database if it has not been initialized yet or if data reset has been requested.
if [ ! -f /botdata/.tn-cookie ] || [ "$RESET_DB" = true ] ; then
	# Run the generator. Save stdout to to a file to extract Tino's password for possible later use.
	./init-db --reset --config=working.config --data=data.json | grep "usr;tino;" > /botdata/tino-password

	# Convert Tino's authentication credentials into a cookie file.
	# The cookie file is also used to check if database has been initialized.
	./credentials.sh < /botdata/tino-password > /botdata/.tn-cookie
fi

# Run the tinode server.
./tinode --config=working.config --static_data=static 2> /var/log/tinode.log
