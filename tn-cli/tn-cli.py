#!/usr/bin/env python

"""Python implementation of Tinode command line client using gRPC."""

# To make print() compatible between p2 and p3
from __future__ import print_function

import argparse
import base64
import grpc
import json
import mimetypes
import os
import pkg_resources
import platform
try:
    import Queue as queue
except ImportError:
    import queue
import random
import re
import requests
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
PROTOCOL_VERSION = "0"
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

# Detect if the tn-cli is running interactively or being piped.
IsInteractive = sys.stdin.isatty()
# Print prompts in interactive mode only.
def printout(*args):
    if IsInteractive:
        print(*args)

def printerr(*args):
    text = ""
    for a in args:
        text = text + str(a) + " "
    # Strip just the spaces here, don't strip the newline or tabs.
    text = text.strip(" ")
    if text:
        sys.stderr.write(text + "\n")

# Default values for user and topic
DefaultUser = None
DefaultTopic = None

# Variables: results of command execution
Variables = {}

# Pack user's name and avatar into a vcard.
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
                mimetype = mimetypes.guess_type(photofile)
                if mimetype[0]:
                    mimetype = mimetype[0].split("/")[1]
                else:
                    mimetype = 'jpeg'
                data = base64.b64encode(f.read())
                # python3 fix.
                if type(data) is not str:
                    data = data.decode()
                card['photo'] = {
                    'data': data,
                    'type': mimetype
                }
                f.close()
            except IOError as err:
                stdoutln("Error opening '" + photofile + "':", err)

    return card

# encode_to_bytes takes an object/dictionary and converts it to json-formatted byte array.
def encode_to_bytes(src):
    if src == None:
        return None
    return json.dumps(src).encode('utf-8')

# Parse credentials
def parse_cred(cred):
    result = None
    if cred != None:
        result = []
        for c in cred.split(","):
            parts = c.split(":")
            result.append(pb.Credential(method=parts[0] if len(parts) > 0 else None,
                value=parts[1] if len(parts) > 1 else None,
                response=parts[2] if len(parts) > 2 else None))

    return result

# Read a value in the server response using dot notation, i.e.
# $user.params.token or $meta.sub[1].user
def getVar(path):
    if not path.startswith("$"):
        return path

    parts = path.split('.')
    if parts[0] not in Variables:
        return None
    var = Variables[parts[0]]
    if len(parts) > 1:
        parts = parts[1:]
        reIndex = re.compile(r"(\w+)\[(\w+)\]")
        for p in parts:
            x = None
            m = reIndex.match(p)
            if m:
                p = m.group(1)
                if m.group(2).isdigit():
                    x = int(m.group(2))
                else:
                    x = m.group(2)
            var = getattr(var, p)
            if x or x == 0:
                var = var[x]
    return var

# Dereference values, i.e. cmd.val == $usr => cmd.val == <actual value of usr>
def derefVals(cmd):
    for key in dir(cmd):
        if not key.startswith("__") and key != 'varname':
            val = getattr(cmd, key)
            if type(val) is str and val.startswith("$"):
                setattr(cmd, key, getVar(val))
    return cmd

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
    try:
        # iter(...) is a workaround for a python2 bug https://bugs.python.org/issue3907
        for cmd in iter(sys.stdin.readline, ''):
            cmd = cmd.strip()
            # Check for continuation symbol \ in the end of the line.
            if len(cmd) > 0 and cmd[-1] == "\\":
                cmd = cmd[:-1].rstrip()
                if cmd:
                    if partial_input:
                        partial_input += " " + cmd
                    else:
                        partial_input = cmd

                if IsInteractive:
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

            InputQueue.put(cmd)

            # Stop processing input
            if cmd == 'exit' or cmd == 'quit' or cmd == '.exit' or cmd == '.quit':
                return

    except Exception as ex:
        printerr("Exception in stdin", ex)

    InputQueue.put("exit")

# Constructing individual messages
# {hi}
def hiMsg(id):
    OnCompletion[str(id)] = lambda params: print_server_params(params)
    return pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent=APP_NAME + "/" + APP_VERSION + " (" +
        platform.system() + "/" + platform.release() + "); gRPC-python/" + LIB_VERSION,
        ver=LIB_VERSION, lang="EN"))

# {acc}
def accMsg(id, cmd):
    if cmd.uname:
        if cmd.password == None:
            cmd.password = ''
        cmd.secret = str(cmd.uname) + ":" + str(cmd.password)

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

# {login}
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

# {sub}
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

# {leave}
def leaveMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic
    return pb.ClientMsg(leave=pb.ClientLeave(id=str(id), topic=cmd.topic, unsub=cmd.unsub), on_behalf_of=DefaultUser)

# {pub}
def pubMsg(id, cmd):
    if not cmd.topic:
        cmd.topic = DefaultTopic

    head = {}
    if cmd.drafty:
        head['mime'] = encode_to_bytes('text/x-drafty')

    # Excplicitly provided 'mime' will override the one assigned above.
    if cmd.head:
        for h in cmd.head.split(","):
            key, val = h.split(":")
            head[key] = encode_to_bytes(val)

    content = json.loads(cmd.drafty) if cmd.drafty else cmd.content

    return pb.ClientMsg(pub=pb.ClientPub(id=str(id), topic=cmd.topic, no_echo=True,
        head=head, content=encode_to_bytes(content)), on_behalf_of=DefaultUser)

# {get}
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

# {set}
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

# {del}
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
        enum_what = pb.ClientDel.USER
        cmd.topic = None

    else:
        stdoutln("Unrecognized delete option '", cmd.what, "'")
        return None

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
        xdel.user_id = cmd.user
    if cmd.topic != None:
        xdel.topic = cmd.topic

    return msg

# {note}
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

# Upload file out of band (not gRPC)
def upload(id, cmd):
    result = requests.post('/v' + PROTOCOL_VERSION + '/file/u/',
        headers = {
            'X-Tinode-APIKey': this._apiKey,
            'X-Tinode-Auth': 'Token ' + this._authToken.token,
            'User-Agent': APP_NAME + " " + APP_VERSION + "/" + LIB_VERSION
        },
        data = {'id': id},
        files = {'file': (cmd.filename, open(cmd.filename, 'rb'))})

    print(result.status_code, result.reason)

    return None

# Given an array of parts, parse commands and arguments
def parse_cmd(parts):
    parser = None
    if parts[0] == "acc":
        parser = argparse.ArgumentParser(prog=parts[0], description='Create or alter an account')
        parser.add_argument('--user', default='new', help='ID of the account to update')
        parser.add_argument('--scheme', default='basic', help='authentication scheme, default=basic')
        parser.add_argument('--secret', default=None, help='secret for authentication')
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
    elif parts[0] == "del":
        parser = argparse.ArgumentParser(prog=parts[0], description='Delete message(s), subscription, topic, user')
        parser.add_argument('what', default=None, help='what to delete')
        parser.add_argument('--topic', default=None, help='topic being affected')
        parser.add_argument('--user', default=None, help='either delete this user or a subscription with this user')
        parser.add_argument('--seq', default=None, help='"all" or comma separated list of message IDs to delete')
        parser.add_argument('--hard', action='store_true', help='request to hard-delete')
    elif parts[0] == "login":
        parser = argparse.ArgumentParser(prog=parts[0], description='Authenticate current session')
        parser.add_argument('secret', nargs='?', default=argparse.SUPPRESS, help='secret for authentication')
        parser.add_argument('--scheme', default='basic', help='authentication schema, default=basic')
        parser.add_argument('--secret', dest='secret', default=None, help='secret for authentication')
        parser.add_argument('--uname', default=None, help='user name in basic authentication scheme')
        parser.add_argument('--password', default=None, help='password in basic authentication scheme')
        parser.add_argument('--cred', default=None, help='credentials, comma separated list in method:value:response format, e.g. email:test@example.com,tel:12345')
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
        parser.add_argument('--head', help='message headers')
        parser.add_argument('--content', dest='content', help='message to send')
        parser.add_argument('--drafty', help='structured message to send, e.g. drafty content')
        parser.add_argument('--image', help='image file to insert into message (not implemented yet)')
        parser.add_argument('--attachment', help='file to send as an attachment (not implemented yet)')
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
        parser.add_argument('--fn', help='topic\'s title')
        parser.add_argument('--photo', help='avatar file name')
        parser.add_argument('--public', help='topic\'s public info, alternative to fn+photo')
        parser.add_argument('--private', help='topic\'s private info')
        parser.add_argument('--auth', help='default access mode for authenticated users')
        parser.add_argument('--anon', help='default access mode for anonymous users')
        parser.add_argument('--user', help='ID of the account to update')
        parser.add_argument('--mode', help='new value of access mode')
        parser.add_argument('--tags', help='tags for topic discovery, comma separated list without spaces')
    elif parts[0] == "note":
        parser = argparse.ArgumentParser(prog=parts[0], description='Send notification to topic, ex "note kp"')
        parser.add_argument('topic', help='topic to notify')
        parser.add_argument('what', nargs='?', default='kp', const='kp', choices=['kp', 'read', 'recv'],
            help='notification type: kp (key press), recv, read - message received or read receipt')
        parser.add_argument('--seq', help='message ID being reported')
    elif parts[0] == "upload":
        parser = argparse.ArgumentParser(prog=parts[0], description='Upload file out of band')
        parser.add_argument('filename', help='name of the file to upload')
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
        parser.add_argument('varname', help='name of the variable to print')

    elif parts[0] == ".sleep":
        parser = argparse.ArgumentParser(prog=parts[0], description='Pause execution')
        parser.add_argument('millis', type=int, help='milliseconds to wait')

    else:
        parser = parse_cmd(parts)

    if not parser:
        printout("Unrecognized:", parts[0])
        printout("Possible commands:")
        printout("\t.await\t- wait for completion of an operation")
        printout("\t.exit\t- exit the program (also .quit)")
        printout("\t.log\t- write value of a variable to stdout")
        printout("\t.must\t- wait for completion of an operation, terminate on failure")
        printout("\t.sleep\t- pause execution")
        printout("\t.use\t- set default user (on_behalf_of) or topic")
        printout("\tacc\t- create or alter an account")
        printout("\tdel\t- delete message(s), topic, subscription, or user")
        printout("\tget\t- query topic for metadata or messages")
        printout("\tleave\t- detach or unsubscribe from topic")
        printout("\tlogin\t- authenticate current session")
        printout("\tnote\t- send a notification")
        printout("\tpub\t- post message to topic")
        printout("\tset\t- update topic metadata")
        printout("\tsub\t- subscribe to topic")
        printout("\tupload\t- upload file out of band")
        printout("\n\tType <command> -h for help")
        return None

    try:
        args = parser.parse_args(parts[1:])
        args.cmd = parts[0]
        args.synchronous = synchronous
        args.failOnError = failOnError
        if varname:
            args.varname = varname
        return args

    except SystemExit:
        return None

# Process command-line input string: execute local commands, generate
# protobuf messages for remote commands.
def serialize_cmd(string, id):
    """Take string read from the command line, convert in into a protobuf message"""
    cmd = {}
    messages = {
        "acc": accMsg,
        "login": loginMsg,
        "sub": subMsg,
        "leave": leaveMsg,
        "pub": pubMsg,
        "get": getMsg,
        "set": setMsg,
        "del": delMsg,
        "note": noteMsg,
        "upload": upload
    }
    try:
        # Convert string into a dictionary
        cmd = parse_input(string)
        if cmd == None:
            return None, None

        # Process dictionary
        if cmd.cmd == ".log":
            stdoutln(getVar(cmd.varname))
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

        elif cmd.cmd == ".sleep":
            stdoutln("Pausing for " + str(cmd.millis) + "ms...")
            time.sleep(cmd.millis/1000.)
            return None, None

        elif cmd.cmd in messages:
            return messages[cmd.cmd](id, derefVals(cmd)), cmd

        else:
            stdoutln("Error: unrecognized: '{0}'".format(cmd.cmd))
            return None, None

    except Exception as err:
        stdoutln("Error in '{0}': {1}".format(cmd.cmd, err))
        return None, None

# Generator of protobuf messages.
def gen_message(scheme, secret):
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

    if scheme != None:
        id += 1
        login = lambda:None
        setattr(login, 'scheme', scheme)
        setattr(login, 'secret', secret)
        setattr(login, 'cred', None)
        yield loginMsg(id, login)

    print_prompt = True

    while True:
        if not WaitingFor and not InputQueue.empty():
            id += 1
            inp = InputQueue.get()

            if inp == 'exit' or inp == 'quit' or inp == '.exit' or inp == '.quit':
                return

            pbMsg, cmd = serialize_cmd(inp, id)
            print_prompt = IsInteractive
            if pbMsg != None:
                if not IsInteractive:
                    sys.stdout.write("=> " + inp + "\n")
                    sys.stdout.flush()

                if cmd.synchronous:
                    cmd.await_ts = time.time()
                    cmd.await_id = str(id)
                    WaitingFor = cmd

                yield pbMsg

        elif not OutputQueue.empty():
            sys.stdout.write("\r<= "+OutputQueue.get())
            sys.stdout.flush()
            print_prompt = IsInteractive

        else:
            if print_prompt:
                sys.stdout.write("tn> ")
                sys.stdout.flush()
                print_prompt = False
            if WaitingFor:
                if time.time() - WaitingFor.await_ts > AWAIT_TIMEOUT:
                    stdoutln("Timeout while waiting for '{0}' response".format(WaitingFor.cmd))
                    WaitingFor = None
            time.sleep(0.1)

# The main processing loop: send messages to server, receive responses.
def run(args, schema, secret):
    global WaitingFor
    global Variables

    try:
        # Create secure channel with default credentials.
        channel = None
        if args.ssl:
            opts = (('grpc.ssl_target_name_override', args.ssl_host),) if args.ssl_host else None
            channel = grpc.secure_channel(args.host, grpc.ssl_channel_credentials(), opts)
        else:
            channel = grpc.insecure_channel(args.host)

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
                    if 'varname' in WaitingFor:
                        Variables[WaitingFor.varname] = msg.ctrl
                    if WaitingFor.failOnError and msg.ctrl.code >= 400:
                        raise Exception(str(msg.ctrl.code) + " " + msg.ctrl.text)
                    WaitingFor = None

                topic = " (" + str(msg.ctrl.topic) + ")" if msg.ctrl.topic else ""
                stdoutln("\r<= " + str(msg.ctrl.code) + " " + msg.ctrl.text + topic)

            elif msg.HasField("meta"):
                what = []
                if len(msg.meta.sub) > 0:
                    what.append("sub")
                if msg.meta.HasField("desc"):
                    what.append("desc")
                if msg.meta.HasField("del"):
                    what.append("del")
                if len(msg.meta.tags) > 0:
                    what.append("tags")
                stdoutln("\r<= meta " + ",".join(what) + " " + msg.meta.topic)

                if WaitingFor and WaitingFor.await_id == msg.meta.id:
                    if 'varname' in WaitingFor:
                        Variables[WaitingFor.varname] = msg.meta
                    WaitingFor = None

            elif msg.HasField("data"):
                stdoutln("\n\rFrom: " + msg.data.from_user_id)
                stdoutln("Topic: " + msg.data.topic)
                stdoutln("Seq: " + str(msg.data.seq_id))
                if msg.data.head:
                    stdoutln("Headers:")
                    for key in msg.data.head:
                        stdoutln("\t" + key + ": "+str(msg.data.head[key]))
                stdoutln(json.loads(msg.data.content))

            elif msg.HasField("pres"):
                pass

            elif msg.HasField("info"):
                what = "unknown"
                if msg.info.what == pb.READ:
                    what = "read"
                elif msg.info.what == pb.RECV:
                    what = "recv"
                elif msg.info.what == pb.KP:
                    what = "kp"
                stdoutln("\rMessage #" + str(msg.info.seq_id) + " " + what +
                    " by " + msg.info.from_user_id + "; topic=" + msg.info.topic + " (" + msg.topic + ")")

            else:
                stdoutln("\rMessage type not handled" + str(msg))

    except grpc.RpcError as err:
        printerr("gRPC failed with {0}: {1}".format(err.code(), err.details()))
    except Exception as ex:
        printerr("Request failed: {0}".format(ex))
        # print(traceback.format_exc())
    finally:
        printout('Shutting down...')
        channel.close()
        if InputThread != None:
            InputThread.join(0.3)

# Read cookie file for logging in with the cookie.
def read_cookie():
    try:
        cookie = open('.tn-cli-cookie', 'r')
        params = json.load(cookie)
        cookie.close()
        return params.get("token")

    except Exception as err:
        println("Missing or invalid cookie file '.tn-cli-cookie'", err)
        return None

# Save cookie to file after successful login.
def save_cookie(params):
    if params == None:
        return

    # Protobuf map 'params' is a map which is not a python object or a dictionary. Convert it.
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

# Log server info.
def print_server_params(params):
    servParams = []
    for p in params:
        servParams.append(p + ": " + json.loads(params[p]))
    stdoutln("\r<= Connected to server: " + "; ".join(servParams))

if __name__ == '__main__':
    """Parse command-line arguments. Extract host name and authentication scheme, if one is provided"""
    version = APP_VERSION + "/" + LIB_VERSION + "; gRPC/" + GRPC_VERSION
    purpose = "Tinode command line client. Version " + version + "."

    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:6061', help='address of Tinode gRPC server')
    parser.add_argument('--web-host', default='localhost:6060', help='address of Tinode web server')
    parser.add_argument('--ssl', action='store_true', help='connect to server over secure connection')
    parser.add_argument('--ssl-host', help='SSL host name to use instead of default (useful for connecting to localhost)')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', action='store_true', help='read token from cookie file and use it for authentication')
    parser.add_argument('--no-login', action='store_true', help='do not login even if cookie file is present')
    parser.add_argument('--api-key', default='AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K', help='API key for file uploads')
    parser.add_argument('--version', action='store_true', help='print version')
    args = parser.parse_args()

    if args.version:
        printout(version)
        exit()

    printout(purpose)
    printout("Secure server" if args.ssl else "Server", "at '"+args.host+"'",
        "SNI="+args.ssl_host if args.ssl_host else "")

    schema = None
    secret = None

    if not args.no_login:
        if args.login_token:
            """Use token to login"""
            schema = 'token'
            secret = args.login_token.encode('acsii')
            printout("Logging in with token", args.login_token)

        elif args.login_basic:
            """Use username:password"""
            schema = 'basic'
            secret = args.login_basic
            printout("Logging in with login:password", args.login_basic)

        else:
            """Try reading the cookie file"""
            try:
                schema = 'token'
                secret = read_cookie()
                printout("Logging in with cookie file")
            except Exception as err:
                printerr("Failed to read authentication cookie", err)

    run(args, schema, secret)
