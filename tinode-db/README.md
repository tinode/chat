# Create Tinode DB in a local RethinkDB Cluster

Compile then run from the command line. It will reset "tinode" database and fill it with some initial data. Data
is loaded from the `data.json` file. The default file creates five users with user names alice, bob, carol, dave,
frank. Passwords are the same as user name with 123 appended, e.g. user `alice` has password `alice123`.

If you don't want test data to be loaded, replace `data.json` content with an empty object `{}`.
