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
if [[ "$UPGRADE_DB" = "true" ]] ; then
	SAMPLE_DATA=""
fi

# If push notifications are enabled, generate client-side firebase config file.
if [ ! -z "$FCM_PUSH_ENABLED" ] ; then
	# Write client config to $STATIC_DIR/firebase-init.js
  cat > $STATIC_DIR/firebase-init.js <<- EOM
const FIREBASE_INIT = {
  apiKey: "$FCM_API_KEY",
  appId: "$FCM_APP_ID",
  messagingSenderId: "$FCM_SENDER_ID",
  projectId: "$FCM_PROJECT_ID",
  messagingVapidKey: "$FCM_VAPID_KEY"
};
EOM
else
	# Create an empty firebase-init.js
	echo "" > $STATIC_DIR/firebase-init.js
fi

if [ ! -z "$IOS_UNIV_LINKS_APP_ID" ] ; then
	# Write config to $STATIC_DIR/apple-app-site-association config file.
  # See https://developer.apple.com/library/archive/documentation/General/Conceptual/AppSearch/UniversalLinks.html for details.
	cat > $STATIC_DIR/apple-app-site-association <<- EOM
{
    "applinks": {
        "apps": [],
        "details": [
            {
                "appID": "$IOS_UNIV_LINKS_APP_ID",
                "paths": [ "*" ]
            }
        ]
    }
}
EOM
fi

init_args=("--reset=${RESET_DB}" "--upgrade=${UPGRADE_DB}" "--config=${CONFIG}")
# Maybe load sample data?
if [ ! -z "$SAMPLE_DATA" ] ; then
	init_args+=("--data=$SAMPLE_DATA")
fi
init_stdout=./init-db-stdout.txt
init_stderr=./init-db-stderr.txt
# Initialize the database if it has not been initialized yet or if data reset/upgrade has been requested.
./init-db "${init_args[@]}" 1>$init_stdout 2>$init_stderr
init_result=$?
cat $init_stderr >&2
if [ $init_result != 0 ]; then
	echo "./init-db failed. Quitting."
	exit 1
fi

# If sample data was provided, try to find Tino password.
if [ ! -z "$SAMPLE_DATA" ] ; then
	grep "usr;tino;" $init_stdout > /botdata/tino-password
fi

# Check if the last line in the output contains the magic string.
grep -q "All done" $init_stderr
if [ $? != 0 ]; then
	echo "Database could not be set up correctly. Quitting."
	exit 1
fi

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
if [ ! -z "$PPROF_URL" ] ; then
	args+=("--pprof_url=$PPROF_URL")
fi

# Create the log directory (/var/log/tinode-`current timestamp`).
# And symlink /var/log/tinode-latest to it.
runid=tinode-`date +%s`
logdir=/var/log/$runid
mkdir -p $logdir
if [ -d /var/log/tinode-latest ]; then
	rm /var/log/tinode-latest
fi
pushd .
cd /var/log
ln -s $runid tinode-latest
popd

# Run the tinode server.
./tinode "${args[@]}" 2> $logdir/tinode.log
