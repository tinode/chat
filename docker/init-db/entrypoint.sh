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

# Run the generator. Save stdout to to a file to extract Tino's password. 
/go/bin/tinode-db --reset --config=/config --data=/go/src/github.com/tinode/chat/tinode-db/data.json | grep "usr;tino;" > /botdata/tino-password

# Convert Tino's authentication credentials into a cookie file. 
/go/src/github.com/tinode/chat/tinode-db/credentials.sh < /botdata/tino-password > /botdata/.tn-cookie

