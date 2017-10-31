"""The Python implementation of the gRPC Tinode client."""

import argparse
import base64
import grpc
import json
import random
import sys

from google.protobuf import json_format

import model_pb2 as pb
import model_pb2_grpc as pbx

VERSION = 13

# Dictionary wich contains lambdas to be executed when server response is received
onCompletion = {}

# Saved topic: default topic name to make keyboard input easier
SavedTopic = None

def hiMsg(id):
    onCompletion[str(id)] = lambda params: print_server_params(params)
    return pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent="tn-cli/0.13 python gRPC", ver=VERSION, lang="EN"))

def loginMsg(id, scheme, secret):
    onCompletion[str(id)] = lambda params: save_cookie(params)
    return pb.ClientMsg(login=pb.ClientLogin(id=str(id), scheme=scheme, secret=secret))

def parse_command(string, id):
    """Parses command line input into a ClientMsg"""
    parts = string.split(" ")
    if parts[0] == "acc":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "login":
        return loginMsg(id, ("basic", "token")[parts[1].find(":") == -1], parts[1])
    elif parts[0] == "sub":
        return pb.ClientMsg(sub=pb.ClientSub(id=str(id), topic=parts[1]))
    elif parts[0] == "leave":
        return pb.ClientMsg(leave=pb.ClientLeave(id=str(id), topic=parts[1]))
    elif parts[0] == "pub":
        return pb.ClientMsg(pub=pb.ClientPub(id=str(id),
            topic=parts[1],
            no_echo=True,
            content=json.dumps(parts[2])))
    elif parts[0] == "get":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "set":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "del":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "note":
        print("Not implemented: " + parts[0])
        return None
    else:
        print("Unrecognized: " + parts[0])
        return None

def gen_message(schema, secret):
    """Generator: reads user input, converts to ClientMsg, and yields"""

    random.seed()
    id = random.randint(10000,60000)

    yield hiMsg(id)

    if schema != None:
        id += 1
        yield loginMsg(id, schema, secret)

    while True:
        id += 1
        inp = raw_input("tn> ")
        if inp == "":
            continue
        if inp == "exit" or inp == "quit":
            return
        cmd = parse_command(inp, id)
        if cmd != None:
            yield cmd


def run(addr, schema, secret):
    channel = grpc.insecure_channel(addr)
    stub = pbx.NodeStub(channel)
    # Call the server
    stream = stub.MessageLoop(gen_message(schema, secret))
    try:
        # Read server responses
        for msg in stream:
            if msg.HasField("ctrl"):
                # Run code on command completion
                func = onCompletion.get(msg.ctrl.id)
                if func != None:
                    del onCompletion[msg.ctrl.id]
                    if msg.ctrl.code >= 200 and msg.ctrl.code < 400:
                        func(msg.ctrl.params)
                print str(msg.ctrl.code) + " " + msg.ctrl.text
            elif msg.HasField("data"):
                print "\nFrom: " + msg.data.from_user_id + ":\n"
                print json.loads(msg.data.content) + "\n"
            elif msg.HasField("pres"):
                pass
            else:
                print "Message type not handled", msg

    except grpc._channel._Rendezvous as err:
        print err

def read_cookie():
    try:
        cookie = open('.tn-cli-cookie', 'r')
        params = json.load(cookie)
        cookie.close()
        if params.get("token") == None:
            return None
        return params

    except Exception as err:
        print "Missing or invalid cookie file '.tn-cli-cookie'", err
        return None

def save_cookie(params):
    if params == None:
        return

    # Protobuf map 'params' is not a python object or dictionary. Convert it.
    nice = {}
    for p in params:
        nice[p] = json.loads(params[p])

    print "Authenticated as ", nice.get('user')

    try:
        cookie = open('.tn-cli-cookie', 'w')
        json.dump(nice, cookie)
        cookie.close()
    except Exception as err:
        print "Failed to save authentication cookie", err

def print_server_params(params):
    print "Connected to server:"
    for p in params:
         print "\t" + p + ": " + json.loads(params[p])

if __name__ == '__main__':
    """Parse command-line arguments. Extract host name and authentication scheme, if one is provided"""
    purpose = "Tinode command line client. Version 0.13."
    print purpose
    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:6061', help='address of Tinode server')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', action='store_true', help='read token from cookie file and use it for authentication')
    args = parser.parse_args()

    print "Server '" + args.host + "'"

    schema = None
    secret = None
    if args.login_cookie:
        """Try reading cookie file"""
        params = read_cookie()
        if params != None:
            schema = 'token'
            secret = base64.b64decode(params.get('token').encode('ascii'))

    if schema == None and args.login_token != None:
        """Use token to login"""
        schema = 'token'
        secret = args.login_token

    if schema == None and args.login_basic != None:
        """Use username:password"""
        schema = basic
        secret = args.login_basic

    run(args.host, schema, secret)
