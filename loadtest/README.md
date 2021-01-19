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

This will be eventually packaged into a docker container.
