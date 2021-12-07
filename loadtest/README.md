# Tinode Load Testing

Content of this directory is for running rudimentary load tests of Tinode server. You need this only if you want to run your own load tests.

## Tsung

The `tsung.xml` is a configuration for [Tsung](http://tsung.erlang-projects.org/). The `tinode.beam` is an erlang binary required by the test to generate base64-encoded user-password pairs. The `tinode.erl` is the source for `tinode.beam` (`erlc tinode.erl` -> `tinode.beam`).

[Install Tsung](http://tsung.erlang-projects.org/user_manual/installation.html), then run the test
```
tsung -f ./tsung.xml start
```

## Gatling

A similar loadtest scenario is also available in Gatling. The configuration file is `loadtest.scala`.
Run it with (after [installing Gatling](https://gatling.io/docs/current/installation/)):
```
gatling.sh -sf . -rsf . -rd "na" -s tinode.Loadtest
```

Currently, three tests are available:

* `tinode.Loadtest`: after connecting to server, retrieves user's subscriptions, and publishes a few messages to them one by one.
* `tinode.MeLoadtest`: attempts to max out `me` topic connections.
* `tinode.SingleTopicLoadtest`: connects to and publishes messages to the specified topic (typically, a group topic).

The script supports passing params via the `JAVA_OPTS` envvar.

Parameter name | Default value | Description
-------------- | ------------- | -------------
`num_sessions` | 10000 | Total number of sessions to connect to the server
`ramp` | 300 | Time period in seconds over which to ramp up the load (`0` to `num_sessions`).
`publish_count` | 10 | Number of messages that a user will publish to a topic it subscribes to.
`publish_interval` | 100 | Maximum period of time a user will wait between publishing subsequent messages to a topic.
`accounts` | users.csv | `tinode.Loadtest` and `tinode.SingleTopicLoadtest` only: Path to CSV file containing user accounts to use in loadtest (in format `username,password[,token]` (`token` field is optional).
`topic` | | `tinode.SingleTopicLoadtest` only: topic name to send load to.
`username` | | `tinode.MeLoadtest` only: user to subscribe to `me` topic.
`password` | | `tinode.MeLoadtest` only: user password.

Examples:
```shell
JAVA_OPTS="-Daccounts=users.csv -Dnum_sessions=100 -Dramp=10" gatling.sh -sf . -rsf . -rd "na" -s tinode.Loadtest
```
Ramps up load to 100 sessions listed in `users.csv` file over 10 seconds.

```shell
JAVA_OPTS="-Dusername=user1 -Dpassword=user1123 -Dnum_sessions=10000 -Dramp=600" gatling.sh -sf . -rsf . -rd "na" -s tinode.MeLoadtest
```
Connects 10000 sessions to `me` topic for `user1` with password `user1123` over 600 seconds.

```shell
JAVA_OPTS="-Dtopic=grpYOrcDwORhPg -Daccounts=users.csv -Dnum_sessions=10000 -Dramp=1000 -Dpublish_count=2 -Dpublish_interval=300" gatling.sh -sf . -rsf . -rd "na" -s tinode.SingleTopicLoadtest
```
Connects 10000 users (specified in `users.csv` file) to `grpYOrcDwORhPg` topic over 1000 seconds. Each user will publish 2 messages with interval up to 300 seconds.

This will be eventually packaged into a docker container.

### Experiments

We have tested our single-server Tinode synthetic setup with 50000 accounts on a standard `t3.xlarge` AWS box (4 vCPUs, 16GiB, 5Gbps network) with the `mysql` backend.
As the load increases, before starting to drop:

* The server can sustain 50000 concurrently connected sessions.
* An individual group topic was able to sustain 1500 concurrent sessions.
