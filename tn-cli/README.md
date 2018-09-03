# Command Line Client for Tinode

This is a command line chat client. It's written in Python as a demonstration of Tinode [gRPC](https://grpc.io) [API](../pbx/).

Python 2.7 or 3.4 or newer is required. PIP 9.0.1 or newer is required. If you are using Python 2.7 install `futures`:
```
$ python -m pip install futures
```

Install tinode gRPC bindings:
```
$ python -m pip install tinode_grpc
```

Run the client from the command line:
```
python tn-cli.py --login-basic=alice:alice123
```

The client takes optional parameters:

 * `--host` is the address of the server to connect to.
 * `--login-basic` is the login:password to be authenticated with.
 * `--login-token` is the token to be authenticated with.
 * `--login-cookie` direct the client to read the token from the cookie file generated during an earlier login.

If multiple `login-XYZ` are provided, `login-cookie` is considered first, then `login-token` then `login-basic`. Authentication with token (and cookie) is much faster than with the username-password pair.
