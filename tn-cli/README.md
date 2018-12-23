# Command Line Client for Tinode

This is a command line chat client. It's written in Python as a demonstration of Tinode [gRPC](https://grpc.io) [API](../pbx/).

Python 2.7 or 3.4 or newer is required. PIP 9.0.1 or newer is required. If you are using Python 2.7 install `futures`:
```
$ python -m pip install futures
```

Install [tinode gRPC](https://pypi.org/project/tinode-grpc/) bindings:
```
$ python -m pip install tinode_grpc
```

Run the client from the command line:
```
python tn-cli.py --login-basic=alice:alice123
```

If you are updating an existent installation, make sure the `tinode_grpc` version matches the [server](../server/) version. Upgrade `tinode_grpc` if needed:
```
python -m pip install --upgrade tinode_grpc==X.XX.XX
```
where `X.XX.XX` is the version number which must match the server version number.

The client takes optional parameters:

 * `--host` is the address of the server to connect to; default is `localhost:6061`.
 * `--ssl` the server requires a secure connection (SSL)
 * `--ssl-host` the domain name to use for SNI if different from the `--host` domain name.
 * `--login-basic` is the login:password to be authenticated with.
 * `--login-token` is the token to be authenticated with.
 * `--login-cookie` direct the client to read the token from the cookie file generated during an earlier login.
 * `--no-login` do not login even if cookie file is present.

If multiple `login-XYZ` are provided, `login-cookie` is considered first, then `login-token` then `login-basic`. Authentication with token (and cookie) is much faster than with the username-password pair.

## Connecting to secure server

If the server is secure, i.e. running as `httpS://tinode.example.com/`, add the `--ssl` option.

If you want to connect to the secure server over a local network or under a different name i.e. as `localhost` instead of  `tinode.example.com` in this example, you must specify the SSL domain name to use, otherwise the server will not be able to find the right SSL certificate:
```
python tn-cli.py --host=localhost:6001 --ssl --ssl-host=tinode.example.com
```


## Crash on shutdown

Python 3 sometimes crashes on shutdown with a message `Fatal Python error: PyImport_GetModuleDict: no module dictionary!`. That happens because Python is buggy: https://bugs.python.org/issue26153
