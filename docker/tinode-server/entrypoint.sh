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

/go/bin/server --config=/config --static_data=/go/src/github.com/tinode/chat/server/static
