#!/bin/bash

BINARY_PATH=$GOPATH/bin
TINODE_BINARY=$BINARY_PATH/server

# Kills and removes any running containers.
cleanup() {
  ./run-cluster.sh stop
  if [ -f "./server" ]; then
    rm ./server
  fi
  docker stop mysql && docker rm mysql && docker network rm tinode-net
}

# Reports failure.
fail() {
  cleanup
  echo "**************************************************"
  printf "Tests Failed: ${@}\n"
  echo "**************************************************"
  exit 1
}

# Reports success.
pass() {
  cleanup
  echo "**************************************************"
  echo "*                       OK                       *"
  echo "**************************************************"
  exit 0
}

# Brings up a mysql docker container.
setup() {
  docker info 1>/dev/null 2>&1 || (echo "docker not running" && return 1)
  docker network create tinode-net || (echo "couldn't create tinode-net docker network" && return 1)
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
  go install -tags mysql -ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`" \
    github.com/tinode/chat/tinode-db \
    github.com/tinode/chat/server && \
  ln -s $TINODE_BINARY
}

# Initializes Tinode database.
init-db() {
  $GOPATH/bin/tinode-db -config=./tinode.conf -data=../tinode-db/data.json
}

wait-for() {
  local port=$1
  while ! nc -z localhost $port; do
    sleep 1
  done
}

# Brings up a three-node Tinode cluster.
run-server() {
  ./run-cluster.sh -s "" start && wait-for 16060
}

send-requests() {
  local port=$1
  local id=$2
  local outfile=$(mktemp /tmp/tinode-${id}.txt)
  pushd .
  cd ../tn-cli
  python tn-cli.py --host=localhost:${port} --no-login < sample-script.txt > $outfile || fail "Test script failed (instance port ${port})"
  popd
  num_positive_responses=`grep -c '<= 20[0-9]' $outfile`
  if [ $num_positive_responses -ne 10 ]; then fail "Instance ${port}: unexpected number of 20* responses: ${num_positive_responses}. Log file ${outfile}"; fi
  rm $outfile
}

# Catch unexpected failures, do cleanup and output an error message
trap 'cleanup ; fail "For Unexpected Reasons"'\
  HUP INT QUIT PIPE TERM

# Normal script termination.
#trap 'cleanup'\
#  EXIT

run_id=`date +%s`
echo "+----------------------------------------------------+"
echo "|                 Tinode sanity test.                |"
echo "+----------------------------------------------------+"
echo "Timestamp = ${run_id}"

setup || fail "Test setup failed."
build || fail "Could not build Tinode binaries"
init-db || fail "Could not initialize Tinode database"
run-server || fail "Could not start tinode"

# Test requests.
send-requests 16060 $run_id
send-requests 16061 $run_id
send-requests 16062 $run_id

pass
