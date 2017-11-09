# Command Line Client for Tinode

This is a command line chat client. It's written in Python as a demonstration of gRPC Tinode API.

Files generated from [protobuf](../pbx/model.proto) are provided for convenience. Files are generated with command
```
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```

The client takes optional parameters:
```
tn-cli --host=localhost:6061 --login-cookie
```

 * `--host` is the address of the server to connect to.
 * `--login-basic` is the login:password to be authenticated with.
 * `--login-token` is the token to be authenticated with.
 * `--login-cookie` direct the client to read the token from the cookie file.

 If multiple `login-XYZ` are provided, `login-cookie` is considered first, then `login-token` then `login-basic`. Authentication with token (and cookie) is much faster than with the username-password pair.
