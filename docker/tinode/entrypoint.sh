#!/bin/bash

# If EXT_CONFIG is set, use it as a config file.
if [ ! -z "$EXT_CONFIG" ] ; then
	CONFIG="$EXT_CONFIG"

	# Enable push notifications.
	if [ ! -z "$FCM_SENDER_ID" ] ; then
		FCM_PUSH_ENABLED=true
	fi

else
	CONFIG=working.config

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

# If external static dir is defined, use it.
# Otherwise, fall back to "./static".
if [ ! -z "$EXT_STATIC_DIR" ] ; then
  STATIC_DIR=$EXT_STATIC_DIR
else
  STATIC_DIR="./static"
fi

# Load default sample data when generating or resetting the database.
if [[ -z "$SAMPLE_DATA" && "$UPGRADE_DB" = "false" ]] ; then
	SAMPLE_DATA="$DEFAULT_SAMPLE_DATA"
fi

# If push notifications are enabled, generate client-side firebase config file.
if [ ! -z "$FCM_PUSH_ENABLED" ] ; then
	# Write client config to $STATIC_DIR/firebase-init.js
	echo "const FIREBASE_INIT={messagingSenderId: \"$FCM_SENDER_ID\", messagingVapidKey: \"$FCM_VAPID_KEY\"};"$'\n' > $STATIC_DIR/firebase-init.js
else
	# Create an empty firebase-init.js
	echo "" > $STATIC_DIR/firebase-init.js
fi

# Initialize the database if it has not been initialized yet or if data reset/upgrade has been requested.
./init-db --reset=${RESET_DB} --upgrade=${UPGRADE_DB} --config=${CONFIG} --data=${SAMPLE_DATA} | grep "usr;tino;" > /botdata/tino-password

if [ -s /botdata/tino-password ] ; then
	# Convert Tino's authentication credentials into a cookie file.
	# The cookie file is also used to check if database has been initialized.

	# /botdata/tino-password could be empty if DB was not updated. In such a case the
	# /botdata/.tn-cookie will not be modified.
	./credentials.sh /botdata/.tn-cookie < /botdata/tino-password
fi

args=("--config=${CONFIG}" "--static_data=$STATIC_DIR")

# Maybe set node name in the cluster.
if [ ! -z "$CLUSTER_SELF" ] ; then
  args+=("--cluster_self=$CLUSTER_SELF")
fi
# Run the tinode server.
./tinode "${args[@]}" 2> /var/log/tinode.log
