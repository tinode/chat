#!/bin/bash

# Check if environment variables (provided as argument list) are set.
function check_vars() {
  local varnames=( "$@" )
  for varname in "${varnames[@]}"
  do
    eval value=\$${varname}
    if [ -z "$value" ] ; then
      echo "$varname env var must be specified."
      exit 1
    fi
  done
}

# Make sure the system uses /etc/hosts when resolving domain names
# (needed for docker-compose's `extra_hosts` param to work correctly).
# See https://github.com/gliderlabs/docker-alpine/issues/367,
# https://github.com/golang/go/issues/35305 for details.
echo "hosts: files dns" > /etc/nsswitch.conf

# Accept http requests at.
LISTEN_AT=":6222"

# Required env vars.
common_vars=( TINODE_ADDR INSTANCE SERVE_FOR )

influx_varnames=( INFLUXDB_VERSION INFLUXDB_ORGANIZATION INFLUXDB_PUSH_INTERVAL \
  INFLUXDB_PUSH_ADDRESS INFLUXDB_AUTH_TOKEN )

prometheus_varnames=( PROM_NAMESPACE PROM_METRICS_PATH )

check_vars "${common_vars[@]}"

# Common arguments.
args=("--tinode_addr=${TINODE_ADDR}" "--instance=${INSTANCE}" "--listen_at=${LISTEN_AT}" "--serve_for=${SERVE_FOR}")

# Platform-specific arguments.
case "$SERVE_FOR" in
"prometheus")
  check_vars "${prometheus_varnames[@]}"
  args+=("--prom_namespace=${PROM_NAMESPACE}" "--prom_metrics_path=${PROM_METRICS_PATH}")
  if [ ! -z "$PROM_TIMEOUT" ]; then
    args+=("--prom_timeout=${PROM_TIMEOUT}")
  fi
  ;;
"influxdb")
  check_vars "${influxdb_varnames[@]}"
  args+=("--influx_db_version=${INFLUXDB_VERSION}" \
         "--influx_organization=${INFLUXDB_ORGANIZATION}" \
         "--influx_push_interval=${INFLUXDB_PUSH_INTERVAL}" \
         "--influx_push_addr=${INFLUXDB_PUSH_ADDRESS}" \
         "--influx_auth_token=${INFLUXDB_AUTH_TOKEN}")
  if [ ! -z "$INFLUXDB_BUCKET" ]; then
    args+=("--influx_bucket=${INFLUXDB_BUCKET}")
  fi
  ;;
*)
  echo "\$SERVE_FOR must be set to either 'prometheus' or 'influxdb'"
  exit 1
  ;;
esac

# Wait for Tinode server if needed.
if [ ! -z "$WAIT_FOR" ] ; then
	IFS=':' read -ra TND <<< "$WAIT_FOR"
	if [ ${#TND[@]} -ne 2 ]; then
		echo "\$WAIT_FOR (${WAIT_FOR}) env var should be in form HOST:PORT"
		exit 1
	fi
	until nc -z -v -w5 ${TND[0]} ${TND[1]}; do echo "waiting for ${WAIT_FOR}..."; sleep 5; done
fi

./exporter "${args[@]}"
