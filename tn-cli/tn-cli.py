#!/usr/bin/env python
# coding=utf-8

"""Python implementation of Tinode command line client using gRPC."""

from __future__ import print_function

import argparse
import os
import platform
import sys

try:
    from importlib.metadata import version
except ImportError:
    # Fallback for Python < 3.8
    from importlib_metadata import version

import tn_globals
from tn_globals import printout
from client import run, read_cookie
from commands import set_macros_module

APP_NAME = "tn-cli"
APP_VERSION = "3.1.0"  # format: 1.9.0b1
LIB_VERSION = version("tinode_grpc")
GRPC_VERSION = version("grpcio")

# This is needed for gRPC SSL to work correctly.
os.environ["GRPC_SSL_CIPHER_SUITES"] = "HIGH+ECDSA"


# Setup crash handler: close input reader otherwise a crash
# makes terminal session unusable.
def exception_hook(type, value, traceBack):
    if tn_globals.InputThread != None:
        tn_globals.InputThread.join(0.3)
sys.excepthook = exception_hook


# Enable the following variables for debugging.
# os.environ["GRPC_TRACE"] = "all"
# os.environ["GRPC_VERBOSITY"] = "INFO"


if __name__ == '__main__':
    """Parse command-line arguments. Extract host name and authentication scheme, if one is provided"""
    version_str = APP_VERSION + "/" + LIB_VERSION + "; gRPC/" + GRPC_VERSION + "; Python " + platform.python_version()
    purpose = "Tinode command line client. Version " + version_str + "."

    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:16060', help='address of Tinode gRPC server')
    parser.add_argument('--web-host', default='localhost:6060', help='address of Tinode web server (for file uploads)')
    parser.add_argument('--ssl', action='store_true', help='connect to server over secure connection')
    parser.add_argument('--ssl-host', help='SSL host name to use instead of default (useful for connecting to localhost)')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', action='store_true', help='read token from cookie file and use it for authentication')
    parser.add_argument('--no-login', action='store_true', help='do not login even if cookie file is present; default in non-interactive (scripted) mode')
    parser.add_argument('--no-cookie', action='store_true', help='do not save login cookie; default in non-interactive (scripted) mode')
    parser.add_argument('--api-key', default='AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K', help='API key for file uploads')
    parser.add_argument('--load-macros', default='./macros.py', help='path to macro module to load')
    parser.add_argument('--version', action='store_true', help='print version')
    parser.add_argument('--verbose', action='store_true', help='log full JSON representation of all messages')
    parser.add_argument('--background', action='store_const', const=True, help='start interactive sessionin background (non-intractive is always in background)')

    args = parser.parse_args()

    if args.version:
        printout(version_str)
        exit()

    if args.verbose:
        tn_globals.Verbose = True

    printout(purpose)
    printout("Secure server" if args.ssl else "Server", "at '"+args.host+"'",
        "SNI="+args.ssl_host if args.ssl_host else "")

    schema = None
    secret = None

    if not args.no_login:
        if args.login_token:
            """Use token to login"""
            schema = 'token'
            secret = args.login_token.encode('ascii')
            printout("Logging in with token", args.login_token)

        elif args.login_basic:
            """Use username:password"""
            schema = 'basic'
            secret = args.login_basic
            printout("Logging in with login:password", args.login_basic)

        elif tn_globals.IsInteractive:
            """Interactive mode only: try reading the cookie file"""
            printout("Logging in with cookie file")
            schema = 'token'
            secret = read_cookie()
            if not secret:
                schema = None

    # Attempt to load the macro file if available.
    macros = None
    if args.load_macros:
        import importlib
        macros = importlib.import_module('macros', args.load_macros) if args.load_macros else None
        set_macros_module(macros)

    # Check if background session is specified explicitly. If not set it to
    # True for non-interactive sessions.
    if args.background is None and not tn_globals.IsInteractive:
        args.background = True

    sys.exit(run(args, schema, secret))
