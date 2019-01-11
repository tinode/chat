#!/usr/bin/env python

"""Python implementation of Tinode command line client using gRPC."""

# To make print() compatible between p2 and p3
from __future__ import print_function

import argparse
import base64
import grpc
import json
import os
import pkg_resources
import platform
try:
    import Queue as queue
except ImportError:
    import queue
import random
import shlex
import sys
import threading
import time

from google.protobuf import json_format

# Import generated grpc modules
from tinode_grpc import pb
from tinode_grpc import pbx

APP_NAME = "tn-cli"
APP_VERSION = "1.2.0"
LIB_VERSION = pkg_resources.get_distribution("tinode_grpc").version
GRPC_VERSION = pkg_resources.get_distribution("grpcio").version

# 5 seconds timeout for .await/.must commands.
AWAIT_TIMEOUT = 5

# This is needed for gRPC SSL to work correctly.
os.environ["GRPC_SSL_CIPHER_SUITES"] = "HIGH+ECDSA"

# Dictionary wich contains lambdas to be executed when server {ctrl} response is received.
OnCompletion = {}

# Outstanding request for a synchronous message.
WaitingFor = None

# IO queues and a thread for asynchronous input/output
InputQueue = queue.Queue()
OutputQueue = queue.Queue()
InputThread = None

# Default values for user and topic
DefaultUser = None
DefaultTopic = None

# Variables: results of command execution
Variables = {}

# Pack user's name and avatar into a vcard represented as json.
def make_vcard(fn, photofile):
    card = None

    if (fn != None and fn.strip() != "") or photofile != None:
        card = {}
        if fn != None:
            card['fn'] = fn.strip()

        if photofile != None:
            try:
                f = open(photofile, 'rb')
                # File extension is used as a file type
                # TODO: use mimetype.guess_type(ext) instead
                card['photo'] = {'data': base64.b64encode(f.read()), 'type': os.path.splitext(photofile)[1]}
            except IOError as err:
                stdoutln("Error opening '" + photofile + "'", err)

    return card

# Parse credentials
def parse_cred(cred):
    result = None
    if cred != None:
        result = []
        for c in cred.split(","):
            parts = c.split(":")
            result.append(pb.Credential(method=parts[0], value=parts[1]))

    return result

# Support for asynchronous input-output to/from stdin/stdout

# Stdout asynchronously writes to sys.stdout
def stdout(*args):
    text = ""
    for a in args:
        text = text + str(a) + " "
    # Strip just the spaces here, don't strip the newline or tabs.
    text = text.strip(" ")
    if text:
        OutputQueue.put(text)

# Stdoutln asynchronously writes to sys.stdout and adds a new line to input.
def stdoutln(*args):
    args = args + ("\n",)
    stdout(*args)

# Stdin reads a possibly multiline input from stdin and queues it for asynchronous processing.
def stdin(InputQueue):
    partial_input = ""
    while True:
        for cmd in sys.stdin.readline().splitlines():
            cmd = cmd.strip()
            # Check for continuation symbol \ in the end of the line.
            if len(cmd) > 0 and cmd[-1] == "\\":
                cmd = cmd[:-1].rstrip()
                if cmd:
                    if partial_input:
                        partial_input += " " + cmd
                    else:
                        partial_input = cmd

                sys.stdout.write("... ")
                sys.stdout.flush()
                continue

            # Check if we have cached input from a previous multiline command.
            if partial_input:
                if cmd:
                    partial_input += " " + cmd
                InputQueue.put(partial_input)
                partial_input = ""
                continue

            if cmd == "":
                continue

            InputQueue.put(cmd)

            # Stop processing input
            if cmd == 'exit' or cmd == 'quit' or cmd == '.exit' or cmd == '.quit':
                return

# encode_to_bytes takes an object/dictionary and converts it to json-formatted byte array.
def encode_to_bytes(src):
    if src == None:
        return None
    return json.dumps(src).encode('utf-8')

# Constructing individual messages
def hiMsg(id):
    OnCompletion[str(id)] = lambda params: print_server_params(params)
    return pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent=APP_NAME + "/" + APP_VERSION + " (" +
        platform.system() + "/" + platform.release() + "); gRPC-python/" + LIB_VERSION,
        ver=LIB_VERSION, lang="EN"))

def accMsg(id, cmd):
    if cmd.uname:
        if cmd.password == None:
            cmd.password = ''
        cmd.secret = str(cmd.uname) + ":" + str(cmd.password)
    elif cmd.asecret:
        cmd.secret = base64.encode(cmd.asecret)

    if cmd.secret:
        cmd.secret = cmd.secret.encode('utf-8')
    else:
        cmd.secret = b''

    cmd.public = encode_to_bytes(make_vcard(cmd.fn, cmd.photo))
    cmd.private = encode_to_bytes(cmd.private)
    return pb.ClientMsg(acc=pb.ClientAcc(id=str(id), user_id=cmd.user,
        scheme=cmd.scheme, secret=cmd.secret, login=cmd.do_login, tags=cmd.tags.split(",") if cmd.tags else None,
        desc=pb.SetDesc(default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon),
            public=cmd.public, private=cmd.private), 
        cred=parse_cred(cmd.cred)), on_behalf_of=DefaultUser)

def loginMsg(id, cmd):
    if cmd.secret == None:
        if cmd.uname == None:
            cmd.uname = ''
        if cmd.password == None:
            cmd.password = ''
        cmd.secret = str(cmd.uname) + ":" + str(cmd.password)
        cmd.secret = cmd.secret.encode('utf-8')
    elif cmd.scheme == "basic":
        # Assuming secret is a uname:password string.
        cmd.secret = str(cmd.secret).encode('utf-8')
    else:
        # All other schemes: assume secret is a base64-encoded string
        cmd.secret = base64.b64decode(cmd.secret)

    msg = pb.ClientMsg(login=pb.ClientLogin(id=str(id), scheme=cmd.scheme, secret=cmd.secret,
        cred=parse_cred(cmd.cred)))
    OnCompletion[str(id)] = lambda params: save_cookie(params)

    return msg

def subMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic
    if cmd.get_query:
        cmd.get_query = pb.GetQuery(what=cmd.get_query.split(",").join(" "))
    cmd.public = encode_to_bytes(make_vcard(cmd.fn, cmd.photo))
    cmd.private = encode_to_bytes(cmd.private)
    return pb.ClientMsg(sub=pb.ClientSub(id=str(id), topic=cmd.topic,
        set_query=pb.SetQuery(
            desc=pb.SetDesc(public=cmd.public, private=cmd.private,
                default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon)),
            sub=pb.SetSub(mode=cmd.mode),
            tags=cmd.tags.split(",") if cmd.tags else None), get_query=cmd.get_query), on_behalf_of=DefaultUser)

def leaveMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic
    return pb.ClientMsg(leave=pb.ClientLeave(id=str(id), topic=cmd.topic, unsub=cmd.unsub), on_behalf_of=DefaultUser)

def pubMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic
    return pb.ClientMsg(pub=pb.ClientPub(id=str(id), topic=cmd.topic, no_echo=True,
                head=cmd.head, content=encode_to_bytes(cmd.content)), on_behalf_of=DefaultUser)

def getMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic

    what = []
    if cmd.desc:
        what.append("desc")
    if cmd.sub:
        what.append("sub")
    if cmd.tags:
        what.append("tags")
    if cmd.data:
        what.append("data")
    return pb.ClientMsg(get=pb.ClientGet(id=str(id), topic=cmd.topic,
        query=pb.GetQuery(what=" ".join(what))), on_behalf_of=DefaultUser)


def setMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic

    if cmd.public == None:
        cmd.public = encode_to_bytes(make_vcard(cmd.fn, cmd.photo))
    else:
        cmd.public = encode_to_bytes(cmd.public)
    cmd.private = encode_to_bytes(cmd.private)
    return pb.ClientMsg(set=pb.ClientSet(id=str(id), topic=cmd.topic,
        query=pb.SetQuery(
            desc=pb.SetDesc(default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon),
                public=cmd.public, private=cmd.private),
        sub=pb.SetSub(user_id=cmd.user, mode=cmd.mode),
        tags=cmd.tags.split(",") if cmd.tags else None)), on_behalf_of=DefaultUser)


def delMsg(id, cmd):
    if not cmd.what:
        stdoutln("Must specify what to delete")
        return None

    cmd.topic = cmd.topic if cmd.topic else DefaultTopic
    cmd.user = cmd.user if cmd.user else DefaultUser
    enum_what = None
    before = None
    seq_list = None
    if cmd.what == 'msg':
        if not cmd.topic:
            stdoutln("Must specify topic to delete messages")
            return None
        enum_what = pb.ClientDel.MSG
        cmd.user = None
        if cmd.msglist == 'all':
            seq_list = [pb.DelQuery(range=pb.SeqRange(low=1, hi=0x8FFFFFF))]
        elif cmd.msglist != None:
            seq_list = [pb.DelQuery(seq_id=int(x.strip())) for x in cmd.msglist.split(',')]

    elif cmd.what == 'sub':
        if not cmd.user or not cmd.topic:
            stdoutln("Must specify topic and user to delete subscription")
            return None
        enum_what = pb.ClientDel.SUB

    elif cmd.what == 'topic':
        if not cmd.topic:
            stdoutln("Must specify topic to delete")
            return None
        enum_what = pb.ClientDel.TOPIC
        cmd.user = None

    elif cmd.what == 'user':
        if not cmd.user:
            stdoutln("Must specify user to delete")
            return None
        enum_what = pb.ClientDel.USER
        cmd.topic = None

    msg = pb.ClientMsg(on_behalf_of=DefaultUser)
    # Field named 'del' conflicts with the keyword 'del. This is a work around.
    xdel = getattr(msg, 'del')
    """
    setattr(msg, 'del', pb.ClientDel(id=str(id), topic=topic, what=enum_what, hard=hard,
        del_seq=seq_list, user_id=user))
    """
    xdel.id = str(id)
    xdel.what = enum_what
    if cmd.hard != None:
        xdel.hard = cmd.hard
    if seq_list != None:
        xdel.del_seq.extend(seq_list)
    if cmd.user != None:
        xdel.user_id = user
    if cmd.topic != None:
        xdel.topic = topic
    return msg

def noteMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic

    enum_what = None
    if cmd.what == 'kp':
        enum_what = pb.KP
        cmd.seq = None
    elif cmd.what == 'read':
        enum_what = pb.READ
        cmd.seq = int(cmd.seq)
    elif what == 'recv':
        enum_what = pb.RECV
        cmd.seq = int(cmd.seq)
    return pb.ClientMsg(note=pb.ClientNote(topic=cmd.topic, what=enum_what, seq_id=cmd.seq), on_behalf_of=DefaultUser)

# Given an array of parts, parse commands and arguments
def parse_cmd(parts):
    parser = None
    if parts[0] == "acc":
        parser = argparse.ArgumentParser(prog=parts[0], description='Create or alter an account')
        parser.add_argument('--user', default='new', help='ID of the account to update')
        parser.add_argument('--scheme', default='basic', help='authentication scheme, default=basic')
        parser.add_argument('--secret', default=None, help='secret for authentication, base64-encoded')
        parser.add_argument('--asecret', default=None, help='secret for authentication, ASCII-encoded')
        parser.add_argument('--uname', default=None, help='user name for basic authentication')
        parser.add_argument('--password', default=None, help='password for basic authentication')
        parser.add_argument('--do-login', action='store_true', help='login with the newly created account')
        parser.add_argument('--tags', action=None, help='tags for user discovery, comma separated list without spaces')
        parser.add_argument('--fn', default=None, help='user\'s human name')
        parser.add_argument('--photo', default=None, help='avatar file name')
        parser.add_argument('--private', default=None, help='user\'s private info')
        parser.add_argument('--auth', default=None, help='default access mode for authenticated users')
        parser.add_argument('--anon', default=None, help='default access mode for anonymous users')
        parser.add_argument('--cred', default=None, help='credentials, comma separated list in method:value format, e.g. email:test@example.com,tel:12345')
    elif parts[0] == "login":
        parser = argparse.ArgumentParser(prog=parts[0], description='Authenticate current session')
        parser.add_argument('--scheme', default='basic', help='authentication schema, default=basic')
        parser.add_argument('secret', nargs='?', default=argparse.SUPPRESS, help='secret for authentication')
        parser.add_argument('--secret', dest='secret', default=None, help='secret for authentication, base64-encoded')
        parser.add_argument('--asecret', default=None, help='secret for authentication, ASCII-encoded')
        parser.add_argument('--uname', default=None, help='user name in basic authentication scheme')
        parser.add_argument('--password', default=None, help='password in basic authentication scheme')
        parser.add_argument('--cred', default=None, help='credentials, comma separated list in method:value format, e.g. email:test@example.com,tel:12345')
    elif parts[0] == "sub":
        parser = argparse.ArgumentParser(prog=parts[0], description='Subscribe to topic')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to subscribe to')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to subscribe to')
        parser.add_argument('--fn', default=None, help='topic\'s user-visible name')
        parser.add_argument('--photo', default=None, help='avatar file name')
        parser.add_argument('--private', default=None, help='topic\'s private info')
        parser.add_argument('--auth', default=None, help='default access mode for authenticated users')
        parser.add_argument('--anon', default=None, help='default access mode for anonymous users')
        parser.add_argument('--mode', default=None, help='new value of access mode')
        parser.add_argument('--tags', default=None, help='tags for topic discovery, comma separated list without spaces')
        parser.add_argument('--get-query', default=None, help='query for topic metadata or messages, comma separated list without spaces')
    elif parts[0] == "leave":
        parser = argparse.ArgumentParser(prog=parts[0], description='Detach or unsubscribe from topic')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to detach from')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to detach from')
        parser.add_argument('--unsub', action='store_true', help='detach and unsubscribe from topic')
    elif parts[0] == "pub":
        parser = argparse.ArgumentParser(prog=parts[0], description='Send message to topic')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to publish to')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to publish to')
        parser.add_argument('content', nargs='?', default=argparse.SUPPRESS, help='message to send')
        parser.add_argument('--content', dest='content', help='message to send')
    elif parts[0] == "get":
        parser = argparse.ArgumentParser(prog=parts[0], description='Query topic for messages or metadata')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to query')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to query')
        parser.add_argument('--desc', action='store_true', help='query topic description')
        parser.add_argument('--sub', action='store_true', help='query topic subscriptions')
        parser.add_argument('--tags', action='store_true', help='query topic tags')
        parser.add_argument('--data', action='store_true', help='query topic messages')
    elif parts[0] == "set":
        parser = argparse.ArgumentParser(prog=parts[0], description='Update topic metadata')
        parser.add_argument('topic', help='topic to update')
        parser.add_argument('--fn', default=None, help='topic\'s title')
        parser.add_argument('--photo', default=None, help='avatar file name')
        parser.add_argument('--public', default=None, help='topic\'s public info, alternative to fn+photo')
        parser.add_argument('--private', default=None, help='topic\'s private info')
        parser.add_argument('--auth', default=None, help='default access mode for authenticated users')
        parser.add_argument('--anon', default=None, help='default access mode for anonymous users')
        parser.add_argument('--user', default=None, help='ID of the account to update')
        parser.add_argument('--mode', default=None, help='new value of access mode')
        parser.add_argument('--tags', default=None, help='tags for topic discovery, comma separated list without spaces')
    elif parts[0] == "del":
        parser = argparse.ArgumentParser(prog=parts[0], description='Delete message(s), subscription, topic, user')
        parser.add_argument('what', default=None, help='what to delete')
        parser.add_argument('--topic', default=None, help='topic being affected')
        parser.add_argument('--user', default=None, help='either delete this user or a subscription with this user')
        parser.add_argument('--seq', default=None, help='"all" or comma separated list of message IDs to delete')
        parser.add_argument('--hard', action='store_true', help='request to hard-delete')
    elif parts[0] == "note":
        parser = argparse.ArgumentParser(prog=parts[0], description='Send notification to topic, ex "note kp"')
        parser.add_argument('topic', help='topic to notify')
        parser.add_argument('what', nargs='?', default='kp', const='kp', choices=['kp', 'read', 'recv'],
            help='notification type: kp (key press), recv, read - message received or read receipt')
        parser.add_argument('--seq', help='message ID being reported')

    return parser

# Parses command line into command and parameters.
def parse_input(cmd):
    # Split line into parts using shell-like syntax.
    parts = shlex.split(cmd, comments=True)
    if len(parts) == 0:
        return None

    parser = None
    varname = None
    synchronous = False
    failOnError = False

    if parts[0] == ".use":
        parser = argparse.ArgumentParser(prog=parts[0], description='Set default user or topic')
        parser.add_argument('--user', default="unchanged", help='ID of default (on_behalf_of) user')
        parser.add_argument('--topic', default="unchanged", help='Name of default topic')
    elif parts[0] == ".await" or parts[0] == ".must":
        # .await|.must [<$variable_name>] <waitable_command> <params>
        if len(parts) > 1:
            synchronous = True
            failOnError = parts[0] == ".must"
            if len(parts) > 2 and parts[1][0] == '$':
                # Varname is given
                varname = parts[1]
                parts = parts[2:]
                parser = parse_cmd(parts)
            else:
                # No varname
                parts = parts[1:]
                parser = parse_cmd(parts)

    elif parts[0] == ".log":
        parser = argparse.ArgumentParser(prog=parts[0], description='Write value of a variable to stdout')
        parser.add_argument('varname', nargs='1', help='name of the variable to print')
    else:
        parser = parse_cmd(parts)

    if not parser:
        print("Unrecognized:", parts[0])
        print("Possible commands:")
        print("\t.await\t- wait for completion of an operation")
        print("\t.exit\t- exit the program (also .quit)")
        print("\t.log\t- write value of a variable to stdout")
        print("\t.must\t- wait for completion of an operation, terminate on failure")
        print("\t.use\t- set default user (on_behalf_of) or topic")
        print("\tacc\t- create or alter an account")
        print("\tdel\t- delete message(s), topic, subscription, or user")
        print("\tget\t- query topic for metadata or messages")
        print("\tleave\t- detach or unsubscribe from topic")
        print("\tlogin\t- authenticate current session")
        print("\tnote\t- send a notification")
        print("\tpub\t- post message to topic")
        print("\tset\t- update topic metadata")
        print("\tsub\t- subscribe to topic")
        print("\n\tType <command> -h for help")
        return None

    try:
        args = parser.parse_args(parts[1:])
        args.cmd = parts[0]
        args.synchronous = synchronous
        args.failOnError = failOnError
        args.varname = varname
        return args

    except SystemExit:
        return None

def serialize_cmd(string, id):
    """Take string read from the command line, convert in into a protobuf message"""
    try:
        # Convert string into a dictionary
        cmd = parse_input(string)
        if cmd == None:
            return None, None

        # Process dictionary
        if cmd.cmd == ".log":
            stdoutln(Variables[cmd.varname])
            return None, None
        elif cmd.cmd == ".use":
            if cmd.user != "unchanged":
                global DefaultUser
                DefaultUser = cmd.user
                stdoutln("Default user='" + DefaultUser + "'")
            if cmd.topic != "unchanged":
                global DefaultTopic
                DefaultTopic = cmd.topic
                stdoutln("Default topic='" + DefaultTopic + "'")
            return None, None
        elif cmd.cmd == "acc":
            return accMsg(id, cmd), cmd
        elif cmd.cmd == "login":
            return loginMsg(id, cmd), cmd
        elif cmd.cmd == "sub":
            return subMsg(id, cmd), cmd
        elif cmd.cmd == "leave":
            return leaveMsg(id, cmd), cmd
        elif cmd.cmd == "pub":
            return pubMsg(id, cmd), cmd
        elif cmd.cmd == "get":
            return getMsg(id, cmd), cmd
        elif cmd.cmd == "set":
            return setMsg(id, cmd), cmd
        elif cmd.cmd == "del":
            return delMsg(id, cmd), cmd
        elif cmd.cmd == "note":
            return noteMsg(id, cmd), cmd
        else:
            stdoutln("Unrecognized: '{0}'".format(cmd.cmd))
            return None, None

    except Exception as err:
        stdoutln("Error in '{0}': {1}".format(cmd.cmd, err))
        return None, None

def gen_message(schema, secret):
    """Client message generator: reads user input as string,
    converts to pb.ClientMsg, and yields"""
    global InputThread
    global WaitingFor

    random.seed()
    id = random.randint(10000,60000)

    # Asynchronous input-output
    InputThread = threading.Thread(target=stdin, args=(InputQueue,))
    InputThread.daemon = True
    InputThread.start()

    yield hiMsg(id)

    if schema != None:
        id += 1
        yield loginMsg(id, schema, secret, None, None, None)

    print_prompt = True

    while True:
        if not WaitingFor and not InputQueue.empty():
            id += 1
            inp = InputQueue.get()
            if inp == 'exit' or inp == 'quit' or inp == '.exit' or inp == '.quit':
                return
            pbMsg, cmd = serialize_cmd(inp, id)
            print_prompt = True
            if pbMsg != None:
                if cmd.synchronous:
                    cmd.await_ts = time.time()
                    cmd.await_id = str(id)
                    WaitingFor = cmd
                yield pbMsg

        elif not OutputQueue.empty():
            sys.stdout.write("\r"+OutputQueue.get())
            sys.stdout.flush()
            print_prompt = True

        else:
            if print_prompt:
                sys.stdout.write("tn> ")
                sys.stdout.flush()
                print_prompt = False
            if WaitingFor:
                if time.time() - WaitingFor.await_ts > AWAIT_TIMEOUT:
                    stdoutln("Timeout while waiting for {0}".format(WaitingFor.cmd))
                    WaitingFor = None
            time.sleep(0.1)

def run(addr, schema, secret, secure, ssl_host):
    global WaitingFor

    try:
        # Create secure channel with default credentials.
        channel = None
        if secure:
            opts = (('grpc.ssl_target_name_override', ssl_host),) if ssl_host else None
            channel = grpc.secure_channel(addr, grpc.ssl_channel_credentials(), opts)
        else:
            channel = grpc.insecure_channel(addr)

        # Call the server
        stream = pbx.NodeStub(channel).MessageLoop(gen_message(schema, secret))

        # Read server responses
        for msg in stream:
            if msg.HasField("ctrl"):
                # Run code on command completion
                func = OnCompletion.get(msg.ctrl.id)
                if func:
                    del OnCompletion[msg.ctrl.id]
                    if msg.ctrl.code >= 200 and msg.ctrl.code < 400:
                        func(msg.ctrl.params)

                if WaitingFor and WaitingFor.await_id == msg.ctrl.id:
                    if WaitingFor.varname:
                        Variables[WaitingFor.varname] = msg.ctrl
                    if WaitingFor.failOnError and msg.ctrl.code >= 400:
                        raise Exception(str(msg.ctrl.code) + " " + msg.ctrl.text)
                    WaitingFor = None

                topic = " (" + str(msg.ctrl.topic) + ")" if msg.ctrl.topic else ""
                stdoutln("\r" + str(msg.ctrl.code) + " " + msg.ctrl.text + topic)

            elif msg.HasField("meta"):
                stdoutln("\n\rMeta: " + str(msg.meta))
                if WaitingFor and WaitingFor.await_id == msg.meta.id:
                    if WaitingFor.varname:
                        Variables[WaitingFor.varname] = msg.meta
                    WaitingFor = None

            elif msg.HasField("data"):
                stdoutln("\n\rFrom: " + msg.data.from_user_id)
                stdoutln("Topic: " + msg.data.topic)
                stdoutln("Seq: " + str(msg.data.seq))
                if msg.data.head:
                    stdoutln("Headers:")
                    for key in msg.data.head:
                        stdoutln("\t" + key + ": "+str(msg.data.head[key]))
                stdoutln(json.loads(msg.data.content))

            elif msg.HasField("pres"):
                pass

            elif msg.HasField("info"):
                user = getattr(msg.info, 'from')
                stdoutln("\rMessage #" + str(msg.info.seq) + " " + msg.info.what +
                    " by " + user + "; topic=" + msg.info.topic + "(" + msg.topic + ")")

            else:
                stdoutln("\rMessage type not handled", msg)

    except grpc.RpcError as err:
        print('gRPC failed with {0}: {1}'.format(err.code(), err.details()))
    except Exception as ex:
        print('Request failed: {0}'.format(ex))
    finally:
        channel.close()
        if InputThread != None:
            InputThread.join(0.3)


def read_cookie():
    try:
        cookie = open('.tn-cli-cookie', 'r')
        params = json.load(cookie)
        cookie.close()
        return params.get("token")

    except Exception as err:
        println("Missing or invalid cookie file '.tn-cli-cookie'", err)
        return None

def save_cookie(params):
    if params == None:
        return

    # Protobuf map 'params' is not a python object or dictionary. Convert it.
    nice = {}
    for p in params:
        nice[p] = json.loads(params[p])

    stdoutln("Authenticated as", nice.get('user'))

    try:
        cookie = open('.tn-cli-cookie', 'w')
        json.dump(nice, cookie)
        cookie.close()
    except Exception as err:
        stdoutln("Failed to save authentication cookie", err)

def print_server_params(params):
    stdoutln("\rConnected to server:")
    for p in params:
         stdoutln("\t" + p + ": " + json.loads(params[p]))

if __name__ == '__main__':
    """Parse command-line arguments. Extract host name and authentication scheme, if one is provided"""
    purpose = "Tinode command line client. Version " + APP_VERSION + "/" + LIB_VERSION + "; gRPC/" + GRPC_VERSION + "."
    print(purpose)
    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:6061', help='address of Tinode server')
    parser.add_argument('--ssl', action='store_true', help='connect to server over secure connection')
    parser.add_argument('--ssl-host', help='SSL host name to use instead of default (useful for connecting to localhost)')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', action='store_true', help='read token from cookie file and use it for authentication')
    parser.add_argument('--no-login', action='store_true', help='do not login even if cookie file is present')
    args = parser.parse_args()

    print("Secure server" if args.ssl else "Server", "at '"+args.host+"'",
        "SNI="+args.ssl_host if args.ssl_host else "")

    schema = None
    secret = None

    if not args.no_login:
        if args.login_token:
            """Use token to login"""
            schema = 'token'
            secret = args.login_token.encode('acsii')
            print("Logging in with token", args.login_token)

        elif args.login_basic:
            """Use username:password"""
            schema = 'basic'
            secret = base64.b64encode(args.login_basic.encode('utf-8'))
            print("Logging in with login:password", args.login_basic)

        else:
            """Try reading the cookie file"""
            try:
                schema = 'token'
                secret = read_cookie()
                print("Logging in with cookie file")
            except Exception as err:
                print("Failed to read authentication cookie", err)

    run(args.host, schema, secret, args.ssl, args.ssl_host)
