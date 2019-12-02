# Monitoring Tinode server

Tinode server can optionally expose runtime statistics as a json document at a configurable HTTP(S) endpoint. The feature is enabled by adding a string parameter `expvar` to the config file. The value of the `expvar` is the URL path pointing to the location where the variables are served. In addition to the config file, the feature can be enabled from the command line by adding an `--expvar` parameter. The feature is disabled if the value of `expvar` is an empty string `""` or a dash `"-"`. A non-blank value of the command line parameter overrides the config file value.

The feature is enabled in the default config file to publish stats at `/debug/vars`.

As of the time of this writing the following stats are published:

* `memstats`: Go's memory statistics as described at https://golang.org/pkg/runtime/#MemStats
* `cmdline`: server's command line parameters as an array of strings.
* `TotalSessions`: the count of all sessions which were created during server's life time.
* `LiveSessions`: the number of sessions currently live, regardless of authentication status.
* `TotalTopics`: the count of all topics activated during servers's life time.
* `LiveTopics`: the number of currently active topics.
