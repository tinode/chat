#!/bin/bash

# Ensure the old config is removed
rm -f working.config

# Generate a new config from template and environment
while IFS='' read -r line || [[ -n $line ]] ; do
    while [[ "$line" =~ (\$[A-Z_][A-Z_0-9]*) ]] ; do
        LHS=${BASH_REMATCH[1]}
        RHS="$(eval echo "\"$LHS\"")"
        line=${line//$LHS/$RHS}
    done
    echo "$line" >> working.config
done < config.template


# Initialize the database if it has not been initialized yet
if [ ! -f /botdata/.tn-cookie ] || [ $RESET_DB ] ; then
	# Run the generator. Save stdout to to a file to extract Tino's password for possible later use.
	./init-db --reset --config=working.config --data=data.json | grep "usr;tino;" > /botdata/tino-password

	# Convert Tino's authentication credentials into a cookie file.
	# The cookie file is also used to check if database has been initialized.
	./credentials.sh < /botdata/tino-password > /botdata/.tn-cookie
fi

# Run the tinode server.
./tinode --config=working.config --static_data=static 2> /var/log/tinode.log
