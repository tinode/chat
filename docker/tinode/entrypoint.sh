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

run_init_db=false
run_tinode=false
case "$SERVICES_TO_RUN" in
"init-db")
  run_init_db=true
  ;;
"tinode")
  run_tinode=true
  ;;
"both")
  run_init_db=true
  run_tinode=true
  ;;
*)
  echo "Invalid val for SERVICES_TO_RUN env var. Can be either 'init-db' or 'tinode' or 'both'."
  exit 1
  ;;
esac

echo "Will run init-db: ${run_init_db}, tinode: ${run_tinode}"

touch /botdata/tino-password

if [ "$run_init_db" == "true" ]; then
  # Initialize the database if it has not been initialized yet or if data reset/upgrade has been requested.
  ./init-db --reset=${RESET_DB} --upgrade=${UPGRADE_DB} --config=${CONFIG} | grep "usr;tino;" > /botdata/tino-password
fi

if [ "$run_tinode" != "true" ]; then
  # If we don't want to run tinode, we are done.
  exit 0
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
