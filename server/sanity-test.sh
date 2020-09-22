#!/bin/bash

# NCurses constants.
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

BINARY_PATH=$GOPATH/bin
TINODE_BINARY=$BINARY_PATH/server

# Kills and removes any running containers.
cleanup() {
  ./run-cluster.sh stop
  if [ -f "./server" ]; then
    rm ./server
  fi
  docker stop mysql && docker rm mysql
}

# Reports failure.
fail() {
  printf "${RED}Tests Failed: ${@}${NC}\n"
  cleanup
  exit 1
}

# Reports success.
pass() {
  printf "${GREEN}OK${NC}\n"
}

# Brings up a mysql docker container.
setup() {
  docker info 1>/dev/null 2>&1 || (echo "docker not running" && return 1) #fail ""
  docker run -p 3306:3306 --name mysql --network tinode-net --env MYSQL_ALLOW_EMPTY_PASSWORD=yes -d mysql:5.7 || return 1
  # This fails to detect when the mysql is actually ready.
  # TODO: figure out why.
  #until nc -z -v -w30 localhost 3306; do
  #  echo "Waiting for database connection..."
  #  sleep 1
  #done
  echo "waiting 10 seconds for mysql to come up..."
  sleep 10
  # Make sure there's no Tinode server binary in the current directory.
  if [ -f "./server" ]; then
    rm ./server
  fi
}

# Compiles Tinode binaries.
build() {
  go build -tags mysql -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`" \
    github.com/tinode/chat/tinode-db \
    github.com/tinode/chat/server && \
  ln -s $TINODE_BINARY
}

# Initializes Tinode database.
init-db() {
  $GOPATH/bin/tinode-db -config=./tinode.conf -data=../tinode-db/data.json  
}

# Brings up a three-node Tinode cluster.
run-server() {
  ./run-cluster.sh -s "" start
}

# Catch unexpected failures, do cleanup and output an error message
trap 'cleanup ; fail "For Unexpected Reasons"'\
  HUP INT QUIT PIPE TERM

# Normal script termination.
trap 'cleanup'\
  EXIT

echo "+----------------------------------------------------+"
echo "|                 Tinode sanity test.                |"
echo "+----------------------------------------------------+"
exit 1
setup || fail "Test setup failed."
build || fail "Could not build Tinode binaries"
init-db || fail "Could not initialize Tinode database"
run-server || fail "Could not start tinode"

# Test requests.
python3 ../tn-cli/tn-cli.py --host=localhost:16060 --no-login < ../tn-cli/sample-script.txt || fail "Test script failed (instance one)"
python3 ../tn-cli/tn-cli.py --host=localhost:16061 --no-login < ../tn-cli/sample-script.txt || fail "Test script failed (instance two)"
python3 ../tn-cli/tn-cli.py --host=localhost:16062 --no-login < ../tn-cli/sample-script.txt || fail "Test script failed (instance three)"

pass
