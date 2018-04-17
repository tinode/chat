#!/bin/bash

# Ensure the old config is removed
rm -f /config

# Generate a new config from template and environment
while IFS='' read -r line || [[ -n $line ]] ; do
    while [[ "$line" =~ (\$[A-Z_][A-Z_0-9]*) ]] ; do
        LHS=${BASH_REMATCH[1]}
        RHS="$(eval echo "\"$LHS\"")"
        line=${line//$LHS/$RHS}
    done
    echo "$line" >> /config
done < /config.template


# Check if user requested to reset database.
if [[ "$1" = "-r" || "$1" = "--reset_db" ]]; then
	rm -f /botdata/.tn-cookie
fi

# Initialize the database if it has not been initialized yet
if [ ! -f /botdata/.tn-cookie ]; then
	# Run the generator. Save stdout to to a file to extract Tino's password for possible later use.
	/go/bin/tinode-db --reset --config=/config --data=/go/src/github.com/tinode/chat/tinode-db/data.json | grep "usr;tino;" > /botdata/tino-password

	# Convert Tino's authentication credentials into a cookie file. 
	# The cookie file is also used to check if database has been initialized.
	/go/src/github.com/tinode/chat/tinode-db/credentials.sh < /botdata/tino-password > /botdata/.tn-cookie
fi

# Run the tinode server.
/go/bin/server --config=/config --static_data=/go/example-react-js 2> log.txt
