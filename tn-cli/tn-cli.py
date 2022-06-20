#!/usr/bin/env python
# coding=utf-8

"""Python implementation of Tinode command line client using gRPC."""

# To make print() compatible between p2 and p3
from __future__ import print_function

import argparse
import base64
import grpc
import json
from PIL import Image
try:
    from io import BytesIO as memory_io
except ImportError:
    from cStringIO import StringIO as memory_io
import mimetypes
import os
import pkg_resources
import platform
from prompt_toolkit import PromptSession
import random
import re
import requests
import shlex
import sys
import threading
import time

# Import generated grpc modules
from tinode_grpc import pb
from tinode_grpc import pbx

import tn_globals
from tn_globals import printerr
from tn_globals import printout
from tn_globals import stdoutln
from tn_globals import to_json

APP_NAME = "tn-cli"
APP_VERSION = "2.0.0b2" # format: 1.9.0b1
PROTOCOL_VERSION = "0"
LIB_VERSION = pkg_resources.get_distribution("tinode_grpc").version
GRPC_VERSION = pkg_resources.get_distribution("grpcio").version

# Maximum in-band (included directly into the message) attachment size which fits into
# a message of 256K in size, assuming base64 encoding and 1024 bytes of overhead.
# This is size of an object *before* base64 encoding is applied.
MAX_INBAND_ATTACHMENT_SIZE = 195840

# Absolute maximum attachment size to be used with the server = 8MB.
MAX_EXTERN_ATTACHMENT_SIZE = 1 << 23

# Maximum allowed linear dimension of an inline image in pixels.
MAX_IMAGE_DIM = 768

# 5 seconds timeout for .await/.must commands.
AWAIT_TIMEOUT = 5

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

# Regex to match and parse subscripted entries in variable paths.
RE_INDEX = re.compile(r"(\w+)\[(\w+)\]")

# Macros module (may be None).
macros = None

# String used as a delete marker. I.e. when a value needs to be deleted, use this string
DELETE_MARKER = 'DEL!'

# Unicode DEL character used internally by Tinode when a value needs to be deleted.
TINODE_DEL = '␡'

# Python is retarded.
class dotdict(dict):
    """dot.notation access to dictionary attributes"""
    __getattr__ = dict.get
    __setattr__ = dict.__setitem__
    __delattr__ = dict.__delitem__


# Pack name, description, and avatar into a theCard.
def makeTheCard(fn, note, photofile):
    card = None

    if (fn != None and fn.strip() != "") or photofile != None or note != None:
        card = {}
        if fn != None:
            fn = fn.strip()
            card['fn'] = TINODE_DEL if fn == DELETE_MARKER or fn == '' else fn

        if note != None:
            note = note.strip()
            card['note'] = TINODE_DEL if note == DELETE_MARKER or note == '' else note

        if photofile != None:
            if photofile == '' or photofile == DELETE_MARKER:
                # Delete the avatar.
                card['photo'] = {
                    'data': TINODE_DEL
                }
            else:
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

# Create drafty representation of a message with an inline image.
def inline_image(filename):
    try:
        im = Image.open(filename, 'r')
        width = im.width
        height = im.height
        format = im.format if im.format else "JPEG"
        if width > MAX_IMAGE_DIM or height > MAX_IMAGE_DIM:
            # Scale the image
            scale = min(min(width, MAX_IMAGE_DIM) / width, min(height, MAX_IMAGE_DIM) / height)
            width = int(width * scale)
            height = int(height * scale)
            resized = im.resize((width, height))
            im.close()
            im = resized

        mimetype = 'image/' + format.lower()
        bitbuffer = memory_io()
        im.save(bitbuffer, format=format)
        data = base64.b64encode(bitbuffer.getvalue())

        # python3 fix.
        if type(data) is not str:
            data = data.decode()

        result = {
            'txt': ' ',
            'fmt': [{'len': 1}],
            'ent': [{'tp': 'IM', 'data':
                {'val': data, 'mime': mimetype, 'width': width, 'height': height,
                    'name': os.path.basename(filename)}}]
        }
        im.close()
        return result
    except IOError as err:
        stdoutln("Failed processing image '" + filename + "':", err)
        return None

# Create a drafty message with an *in-band* attachment.
def attachment(filename):
    try:
        f = open(filename, 'rb')
        # Try to guess the mime type.
        mimetype = mimetypes.guess_type(filename)
        data = base64.b64encode(f.read())
        # python3 fix.
        if type(data) is not str:
            data = data.decode()
        result = {
            'fmt': [{'at': -1}],
            'ent': [{'tp': 'EX', 'data':{
                'val': data, 'mime': mimetype, 'name':os.path.basename(filename)
            }}]
        }
        f.close()
        return result
    except IOError as err:
        stdoutln("Error processing attachment '" + filename + "':", err)
        return None

# encode_to_bytes converts the 'src' to a byte array.
# An object/dictionary is first converted to json string then it's converted to bytes.
# A string is directly converted to bytes.
def encode_to_bytes(src):
    if src == None:
        return None
    if isinstance(src, str):
        return src.encode('utf-8')
    return json.dumps(src).encode('utf-8')

# Parse credentials
def parse_cred(cred):
    result = None
    if cred != None:
        result = []
        for c in cred.split(","):
            parts = c.split(":")
            result.append(pb.ClientCred(method=parts[0] if len(parts) > 0 else None,
                value=parts[1] if len(parts) > 1 else None,
                response=parts[2] if len(parts) > 2 else None))

    return result

# Parse trusted values: [staff,rm-verified].
def parse_trusted(trusted):
    result = None
    if trusted != None:
        result = {}
        for t in trusted.split(","):
            t = t.strip()
            if t.startswith("rm-"):
                result[t[3:]] = TINODE_DEL
            else:
                result[t] = True

    return result

# Read a value in the server response using dot notation, i.e.
# $user.params.token or $meta.sub[1].user
def getVar(path):
    if not path.startswith("$"):
        return path

    parts = path.split('.')
    if parts[0] not in tn_globals.Variables:
        return None
    var = tn_globals.Variables[parts[0]]
    if len(parts) > 1:
        parts = parts[1:]
        for p in parts:
            x = None
            m = RE_INDEX.match(p)
            if m:
                p = m.group(1)
                if m.group(2).isdigit():
                    x = int(m.group(2))
                else:
                    x = m.group(2)
            var = getattr(var, p)
            if x or x == 0:
                var = var[x]
    if isinstance(var, bytes):
      var = var.decode('utf-8')
    return var

# Dereference values, i.e. cmd.val == $usr => cmd.val == <actual value of usr>
def derefVals(cmd):
    for key in dir(cmd):
        if not key.startswith("__") and key != 'varname':
            val = getattr(cmd, key)
            if type(val) is str and val.startswith("$"):
                setattr(cmd, key, getVar(val))
    return cmd


# Prints prompt and reads lines from stdin.
def readLinesFromStdin():
    if tn_globals.IsInteractive:
        while True:
            try:
                line = tn_globals.Prompt.prompt()
                yield line
            except EOFError as e:
                # Ctrl+D.
                break
    else:
        # iter(...) is a workaround for a python2 bug https://bugs.python.org/issue3907
        for cmd in iter(sys.stdin.readline, ''):
            yield cmd


# Stdin reads a possibly multiline input from stdin and queues it for asynchronous processing.
def stdin(InputQueue):
    partial_input = ""
    try:
        for cmd in readLinesFromStdin():
            cmd = cmd.strip()
            # Check for continuation symbol \ in the end of the line.
            if len(cmd) > 0 and cmd[-1] == "\\":
                cmd = cmd[:-1].rstrip()
                if cmd:
                    if partial_input:
                        partial_input += " " + cmd
                    else:
                        partial_input = cmd

                if tn_globals.IsInteractive:
                    sys.stdout.write("... ")
                    sys.stdout.flush()

                continue

            # Check if we have cached input from a previous multiline command.
            if partial_input:
                if cmd:
                    partial_input += " " + cmd
                InputQueue.append(partial_input)
                partial_input = ""
                continue

            InputQueue.append(cmd)

            # Stop processing input
            if cmd == 'exit' or cmd == 'quit' or cmd == '.exit' or cmd == '.quit':
                return

    except Exception as ex:
        printerr("Exception in stdin", ex)

    InputQueue.append('exit')

# Constructing individual messages
# {hi}
def hiMsg(id, background):
    tn_globals.OnCompletion[str(id)] = lambda params: print_server_params(params)
    return pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent=APP_NAME + "/" + APP_VERSION + " (" +
        platform.system() + "/" + platform.release() + "); gRPC-python/" + LIB_VERSION + "+" + GRPC_VERSION,
        ver=LIB_VERSION, lang="EN", background=background))

# {acc}
def accMsg(id, cmd, ignored):
    if cmd.uname:
        cmd.scheme = 'basic'
        if cmd.password == None:
            cmd.password = ''
        cmd.secret = str(cmd.uname) + ":" + str(cmd.password)

    if cmd.secret:
        if cmd.scheme == None:
            cmd.scheme = 'basic'
        cmd.secret = cmd.secret.encode('utf-8')
    else:
        cmd.secret = b''

    state = None
    if cmd.suspend == 'true':
        state = 'susp'
    elif cmd.suspend == 'false':
        state = 'ok'

    cmd.public = encode_to_bytes(makeTheCard(cmd.fn, cmd.note, cmd.photo))
    cmd.private = encode_to_bytes(cmd.private)
    return pb.ClientMsg(acc=pb.ClientAcc(id=str(id), user_id=cmd.user, state=state,
        scheme=cmd.scheme, secret=cmd.secret, login=cmd.do_login, tags=cmd.tags.split(",") if cmd.tags else None,
        desc=pb.SetDesc(default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon),
            public=cmd.public, private=cmd.private, trusted=parse_trusted(cmd.trusted)),
        cred=parse_cred(cmd.cred)),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {login}
def loginMsg(id, cmd, args):
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

    if args.no_cookie or not tn_globals.IsInteractive:
        tn_globals.OnCompletion[str(id)] = lambda params: handle_login(params)
    else:
        tn_globals.OnCompletion[str(id)] = lambda params: save_cookie(params)

    return msg

# {sub}
def subMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic
    if cmd.get_query:
        cmd.get_query = pb.GetQuery(what=" ".join(cmd.get_query.split(",")))
    cmd.public = encode_to_bytes(makeTheCard(cmd.fn, cmd.note, cmd.photo))
    cmd.private = TINODE_DEL if cmd.private == DELETE_MARKER else encode_to_bytes(cmd.private)
    return pb.ClientMsg(sub=pb.ClientSub(id=str(id), topic=cmd.topic,
        set_query=pb.SetQuery(
            desc=pb.SetDesc(public=cmd.public, private=cmd.private, trusted=parse_trusted(cmd.trusted),
                default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon)),
            sub=pb.SetSub(mode=cmd.mode),
            tags=cmd.tags.split(",") if cmd.tags else None),
        get_query=cmd.get_query),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {leave}
def leaveMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic
    return pb.ClientMsg(leave=pb.ClientLeave(id=str(id), topic=cmd.topic, unsub=cmd.unsub),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {pub}
def pubMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic

    head = {}
    if cmd.drafty or cmd.image or cmd.attachment:
        head['mime'] = encode_to_bytes('text/x-drafty')

    # Excplicitly provided 'mime' will override the one assigned above.
    if cmd.head:
        for h in cmd.head.split(","):
            key, val = h.split(":")
            head[key] = encode_to_bytes(val)

    content = json.loads(cmd.drafty) if cmd.drafty \
        else inline_image(cmd.image) if cmd.image \
        else attachment(cmd.attachment) if cmd.attachment \
        else cmd.content

    if not content:
        return None

    return pb.ClientMsg(pub=pb.ClientPub(id=str(id), topic=cmd.topic, no_echo=True,
        head=head, content=encode_to_bytes(content)),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {get}
def getMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic

    what = []
    if cmd.desc:
        what.append("desc")
    if cmd.sub:
        what.append("sub")
    if cmd.tags:
        what.append("tags")
    if cmd.data:
        what.append("data")
    if cmd.cred:
        what.append("cred")
    return pb.ClientMsg(get=pb.ClientGet(id=str(id), topic=cmd.topic,
        query=pb.GetQuery(what=" ".join(what))),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {set}
def setMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic

    if cmd.public == None:
        cmd.public = encode_to_bytes(makeTheCard(cmd.fn, cmd.note, cmd.photo))
    else:
        cmd.public = TINODE_DEL if cmd.public == DELETE_MARKER else encode_to_bytes(cmd.public)
    cmd.private = TINODE_DEL if cmd.private == DELETE_MARKER else encode_to_bytes(cmd.private)
    cred = parse_cred(cmd.cred)
    if cred:
        if len(cred) > 1:
            stdoutln('Warning: multiple credentials specified. Will use only the first one.')
        cred = cred[0]

    return pb.ClientMsg(set=pb.ClientSet(id=str(id), topic=cmd.topic,
        query=pb.SetQuery(
            desc=pb.SetDesc(default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon),
                public=cmd.public, private=cmd.private, trusted=parse_trusted(cmd.trusted)),
        sub=pb.SetSub(user_id=cmd.user, mode=cmd.mode),
        tags=cmd.tags.split(",") if cmd.tags else None,
        cred=cred)),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# {del}
def delMsg(id, cmd, ignored):
    if not cmd.what:
        stdoutln("Must specify what to delete")
        return None

    enum_what = None
    before = None
    seq_list = None
    cred = None
    if cmd.what == 'msg':
        enum_what = pb.ClientDel.MSG
        cmd.topic = cmd.topic if cmd.topic else tn_globals.DefaultTopic
        if not cmd.topic:
            stdoutln("Must specify topic to delete messages")
            return None
        if cmd.user:
            stdoutln("Unexpected '--user' parameter")
            return None
        if not cmd.seq:
            stdoutln("Must specify message IDs to delete")
            return None

        if cmd.seq == 'all':
            seq_list = [pb.SeqRange(low=1, hi=0x8FFFFFF)]
        else:
            # Split a list like '1,2,3,10-22' into ranges.
            try:
                seq_list = []
                for item in cmd.seq.split(','):
                    if '-' in item:
                        low, hi = [int(x.strip()) for x in item.split('-')]
                        if low>=hi or low<=0:
                            stdoutln("Invalid message ID range {0}-{1}".format(low, hi))
                            return None
                        seq_list.append(pb.SeqRange(low=low, hi=hi))
                    else:
                        seq_list.append(pb.SeqRange(low=int(item.strip())))
            except ValueError as err:
                stdoutln("Invalid message IDs: {0}".format(err))
                return None

    elif cmd.what == 'sub':
        cmd.topic = cmd.topic if cmd.topic else tn_globals.DefaultTopic
        cmd.user = cmd.user if cmd.user else tn_globals.DefaultUser
        if not cmd.user or not cmd.topic:
            stdoutln("Must specify topic and user to delete subscription")
            return None
        enum_what = pb.ClientDel.SUB

    elif cmd.what == 'topic':
        cmd.topic = cmd.topic if cmd.topic else tn_globals.DefaultTopic
        if cmd.user:
            stdoutln("Unexpected '--user' parameter")
            return None
        if not cmd.topic:
            stdoutln("Must specify topic to delete")
            return None
        enum_what = pb.ClientDel.TOPIC

    elif cmd.what == 'user':
        cmd.user = cmd.user if cmd.user else tn_globals.DefaultUser
        if cmd.topic:
            stdoutln("Unexpected '--topic' parameter")
            return None
        enum_what = pb.ClientDel.USER

    elif cmd.what == 'cred':
        if cmd.user:
            stdoutln("Unexpected '--user' parameter")
            return None
        if cmd.topic != 'me':
            stdoutln("Topic must be 'me'")
            return None
        cred = parse_cred(cmd.cred)
        if cred is None:
            stdoutln("Failed to parse credential '{0}'".format(cmd.cred))
            return None
        cred = cred[0]
        enum_what = pb.ClientDel.CRED

    else:
        stdoutln("Unrecognized delete option '", cmd.what, "'")
        return None

    msg = pb.ClientMsg(extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))
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
    if cred != None:
        xdel.cred.MergeFrom(cred)

    return msg

# {note}
def noteMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic

    enum_what = None
    cmd.seq = int(cmd.seq)
    if cmd.what == 'kp':
        enum_what = pb.KP
        cmd.seq = None
    elif cmd.what == 'read':
        enum_what = pb.READ
    elif cmd.what == 'recv':
        enum_what = pb.RECV
    elif cmd.what == 'call':
        enum_what = pb.CALL

    enum_event = None
    if enum_what == pb.CALL:
        if cmd.what == 'accept':
            enum_event = pb.ACCEPT
        elif cmd.what == 'answer':
            enum_event = pb.ANSWER
        elif cmd.what == 'ice-candidate':
            enum_event = pb.ICE_CANDIDATE
        elif cmd.what == 'hang-up':
            enum_event = pb.HANG_UP
        elif cmd.what == 'offer':
            enum_event = pb.OFFER
        elif cmd.what == 'ringing':
            enum_event = pb.RINGING
    else:
        cmd.payload = None

    return pb.ClientMsg(note=pb.ClientNote(topic=cmd.topic, what=enum_what,
        seq_id=cmd.seq, event=enum_event, payload=cmd.payload),
        extra=pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser))

# Upload file out of band over HTTP(S) (not gRPC).
def upload(id, cmd, args):
    try:
        scheme = 'https' if args.ssl else 'http'
        result = requests.post(
            scheme + '://' + args.web_host + '/v' + PROTOCOL_VERSION + '/file/u/',
            headers = {
                'X-Tinode-APIKey': args.api_key,
                'X-Tinode-Auth': 'Token ' + tn_globals.AuthToken,
                'User-Agent': APP_NAME + " " + APP_VERSION + "/" + LIB_VERSION
            },
            data = {'id': id},
            files = {'file': (cmd.filename, open(cmd.filename, 'rb'))})
        handle_ctrl(dotdict(json.loads(result.text)['ctrl']))

    except Exception as ex:
        stdoutln("Failed to upload '{0}'".format(cmd.filename), ex)

    return None


# Given an array of parts, parse commands and arguments
def parse_cmd(parts):
    parser = None
    if parts[0] == "acc":
        parser = argparse.ArgumentParser(prog=parts[0], description='Create or alter an account')
        parser.add_argument('--user', default='new', help='ID of the account to update')
        parser.add_argument('--scheme', default=None, help='authentication scheme, default=basic')
        parser.add_argument('--secret', default=None, help='secret for authentication')
        parser.add_argument('--uname', default=None, help='user name for basic authentication')
        parser.add_argument('--password', default=None, help='password for basic authentication')
        parser.add_argument('--do-login', action='store_true', help='login with the newly created account')
        parser.add_argument('--tags', action=None, help='tags for user discovery, comma separated list without spaces')
        parser.add_argument('--fn', default=None, help='user\'s human name')
        parser.add_argument('--photo', default=None, help='avatar file name')
        parser.add_argument('--private', default=None, help='user\'s private info')
        parser.add_argument('--note', default=None, help='user\'s description')
        parser.add_argument('--trusted', default=None, help='trusted markers: verified, staff, danger, prepend with rm- to remove, e.g. rm-verified')
        parser.add_argument('--auth', default=None, help='default access mode for authenticated users')
        parser.add_argument('--anon', default=None, help='default access mode for anonymous users')
        parser.add_argument('--cred', default=None, help='credentials, comma separated list in method:value format, e.g. email:test@example.com,tel:12345')
        parser.add_argument('--suspend', default=None, help='true to suspend the account, false to un-suspend')
    elif parts[0] == "del":
        parser = argparse.ArgumentParser(prog=parts[0], description='Delete message(s), subscription, topic, user')
        parser.add_argument('what', default=None, help='what to delete')
        parser.add_argument('--topic', default=None, help='topic being affected')
        parser.add_argument('--user', default=None, help='either delete this user or a subscription with this user')
        parser.add_argument('--seq', default=None, help='"all" or a list of comma- and dash-separated message IDs to delete, e.g. "1,2,9-12"')
        parser.add_argument('--hard', action='store_true', help='request to hard-delete')
        parser.add_argument('--cred', help='credential to delete in method:value format, e.g. email:test@example.com, tel:12345')
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
        parser.add_argument('--note', default=None, help='topic\'s description')
        parser.add_argument('--trusted', default=None, help='trusted markers: verified, staff, danger')
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
        parser.add_argument('--cred', action='store_true', help='query account credentials')
    elif parts[0] == "set":
        parser = argparse.ArgumentParser(prog=parts[0], description='Update topic metadata')
        parser.add_argument('topic', help='topic to update')
        parser.add_argument('--fn', help='topic\'s title')
        parser.add_argument('--photo', help='avatar file name')
        parser.add_argument('--public', help='topic\'s public info, alternative to fn+photo+note')
        parser.add_argument('--private', help='topic\'s private info')
        parser.add_argument('--note', default=None, help='topic\'s description')
        parser.add_argument('--trusted', default=None, help='trusted markers: verified, staff, danger')
        parser.add_argument('--auth', help='default access mode for authenticated users')
        parser.add_argument('--anon', help='default access mode for anonymous users')
        parser.add_argument('--user', help='ID of the account to update')
        parser.add_argument('--mode', help='new value of access mode')
        parser.add_argument('--tags', help='tags for topic discovery, comma separated list without spaces')
        parser.add_argument('--cred', help='credential to add in method:value format, e.g. email:test@example.com, tel:12345')
    elif parts[0] == "note":
        parser = argparse.ArgumentParser(prog=parts[0], description='Send notification to topic, ex "note kp"')
        parser.add_argument('topic', help='topic to notify')
        parser.add_argument('what', nargs='?', default='kp', const='kp', choices=['call', 'kp', 'read', 'recv'],
            help='notification type: kp (key press), recv, read - message received or read receipt')
        parser.add_argument('--seq', help='message ID being reported')
        parser.add_argument('--event', help='video call event', choices=['accept', 'answer', 'ice-candidate', 'hang-up', 'offer', 'ringing'])
        parser.add_argument('--payload', help='video call payload')
    elif parts[0] == "upload":
        parser = argparse.ArgumentParser(prog=parts[0], description='Upload file out of band')
        parser.add_argument('filename', help='name of the file to upload')
    elif macros:
        parser = macros.parse_macro(parts)
    return parser

# Parses command line into command and parameters.
def parse_input(cmd):
    # Split line into parts using shell-like syntax.
    try:
        parts = shlex.split(cmd, comments=True)
    except Exception as err:
        printout('Error parsing command: ', err)
        return None
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

    elif parts[0] == ".verbose":
        parser = argparse.ArgumentParser(prog=parts[0], description='Toggle logging verbosity')

    elif parts[0] == ".delmark":
        parser = argparse.ArgumentParser(prog=parts[0], description='Use custom delete maker instead of default DEL!')
        parser.add_argument('delmark', help='marker to use')

    else:
        parser = parse_cmd(parts)

    if not parser:
        printout("Unrecognized:", parts[0])
        printout("Possible commands:")
        printout("\t.await\t\t- wait for completion of an operation")
        printout("\t.delmark\t- custom delete marker to use instead of default DEL!")
        printout("\t.exit\t\t- exit the program (also .quit)")
        printout("\t.log\t\t- write value of a variable to stdout")
        printout("\t.must\t\t- wait for completion of an operation, terminate on failure")
        printout("\t.sleep\t\t- pause execution")
        printout("\t.use\t\t- set default user (on_behalf_of) or topic")
        printout("\t.verbose\t- toggle logging verbosity on/off")
        printout("\tacc\t\t- create or alter an account")
        printout("\tdel\t\t- delete message(s), topic, subscription, or user")
        printout("\tget\t\t- query topic for metadata or messages")
        printout("\tleave\t\t- detach or unsubscribe from topic")
        printout("\tlogin\t\t- authenticate current session")
        printout("\tnote\t\t- send a notification")
        printout("\tpub\t\t- post message to topic")
        printout("\tset\t\t- update topic metadata")
        printout("\tsub\t\t- subscribe to topic")
        printout("\tupload\t\t- upload file out of band")
        printout("\tusermod\t\t- modify user account")
        printout("\n\tType <command> -h for help")

        if macros:
            printout("\nMacro commands:")
            for key in sorted(macros.Macros):
                macro = macros.Macros[key]
                printout("\t%s\t\t- %s" % (macro.name(), macro.description()))
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
def serialize_cmd(string, id, args):
    """Take string read from the command line, convert in into a protobuf message"""
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
                if cmd.user:
                    if len(cmd.user) > 3 and cmd.user.startswith("usr"):
                        tn_globals.DefaultUser = cmd.user
                    else:
                        stdoutln("Error: user ID '{}' is invalid".format(cmd.user))
                else:
                    tn_globals.DefaultUser = None
                stdoutln("Default user='{}'".format(tn_globals.DefaultUser))

            if cmd.topic != "unchanged":
                if cmd.topic:
                    if cmd.topic[:3] in ['me', 'fnd', 'sys', 'usr', 'grp', 'chn']:
                        tn_globals.DefaultTopic = cmd.topic
                    else:
                        stdoutln("Error: topic '{}' is invalid".format(cmd.topic))
                else:
                    tn_globals.DefaultTopic = None
                stdoutln("Default topic='{}'".format(tn_globals.DefaultTopic))

            return None, None

        elif cmd.cmd == ".sleep":
            stdoutln("Pausing for {}ms...".format(cmd.millis))
            time.sleep(cmd.millis/1000.)
            return None, None

        elif cmd.cmd == ".verbose":
            tn_globals.Verbose = not tn_globals.Verbose
            stdoutln("Logging is {}".format("verbose" if tn_globals.Verbose else "normal"))
            return None, None

        elif cmd.cmd == ".delmark":
            DELETE_MARKER = cmd.delmark
            stdoutln("Using {} as delete marker".format(DELETE_MARKER))
            return None, None

        elif cmd.cmd == "upload":
            # Start async upload
            upload_thread = threading.Thread(target=upload, args=(id, derefVals(cmd), args), name="Uploader_"+cmd.filename)
            upload_thread.start()
            cmd.no_yield = True
            return True, cmd

        elif cmd.cmd in messages:
            return messages[cmd.cmd](id, derefVals(cmd), args), cmd
        elif macros and cmd.cmd in macros.Macros:
            return True, macros.Macros[cmd.cmd].run(id, derefVals(cmd), args)

        else:
            stdoutln("Error: unrecognized: '{}'".format(cmd.cmd))
            return None, None

    except Exception as err:
        stdoutln("Error in '{0}': {1}".format(cmd.cmd, err))
        return None, None

def pop_from_output_queue():
    if tn_globals.OutputQueue.empty():
        return False
    sys.stdout.write("\r<= "+tn_globals.OutputQueue.get())
    sys.stdout.flush()
    return True

# Generator of protobuf messages.
def gen_message(scheme, secret, args):
    """Client message generator: reads user input as string,
    converts to pb.ClientMsg, and yields"""
    random.seed()
    id = random.randint(10000,60000)

    # Asynchronous input-output
    tn_globals.InputThread = threading.Thread(target=stdin, args=(tn_globals.InputQueue,))
    tn_globals.InputThread.daemon = True
    tn_globals.InputThread.start()

    msg = hiMsg(id, args.background)
    if tn_globals.Verbose:
        stdoutln("\r=> " + to_json(msg))
    yield msg

    if scheme != None:
        id += 1
        login = lambda:None
        setattr(login, 'scheme', scheme)
        setattr(login, 'secret', secret)
        setattr(login, 'cred', None)
        msg = loginMsg(id, login, args)
        if tn_globals.Verbose:
            stdoutln("\r=> " + to_json(msg))
        yield msg

    print_prompt = True

    while True:
        try:
            if not tn_globals.WaitingFor and tn_globals.InputQueue:
                id += 1
                inp = tn_globals.InputQueue.popleft()

                if inp == 'exit' or inp == 'quit' or inp == '.exit' or inp == '.quit':
                    # Drain the output queue.
                    while pop_from_output_queue():
                        pass
                    return

                pbMsg, cmd = serialize_cmd(inp, id, args)
                print_prompt = tn_globals.IsInteractive
                if isinstance(cmd, list):
                    # Push the expanded macro back on the command queue.
                    tn_globals.InputQueue.extendleft(reversed(cmd))
                    continue
                if pbMsg != None:
                    if not tn_globals.IsInteractive:
                        sys.stdout.write("=> " + inp + "\n")
                        sys.stdout.flush()

                    if cmd.synchronous:
                        cmd.await_ts = time.time()
                        cmd.await_id = str(id)
                        tn_globals.WaitingFor = cmd

                    if not hasattr(cmd, 'no_yield'):
                        if tn_globals.Verbose:
                            stdoutln("\r=> " + to_json(pbMsg))
                        yield pbMsg

            elif not tn_globals.OutputQueue.empty():
                pop_from_output_queue()
                print_prompt = tn_globals.IsInteractive

            else:
                if print_prompt:
                    sys.stdout.write("tn> ")
                    sys.stdout.flush()
                    print_prompt = False
                if tn_globals.WaitingFor:
                    if time.time() - tn_globals.WaitingFor.await_ts > AWAIT_TIMEOUT:
                        stdoutln("Timeout while waiting for '{0}' response".format(tn_globals.WaitingFor.cmd))
                        tn_globals.WaitingFor = None

                if tn_globals.IsInteractive:
                    time.sleep(0.1)
                else:
                    time.sleep(0.01)

        except Exception as err:
            stdoutln("Exception in generator: {0}".format(err))


# Handle {ctrl} server response
def handle_ctrl(ctrl):
    # Run code on command completion
    func = tn_globals.OnCompletion.get(ctrl.id)
    if func:
        del tn_globals.OnCompletion[ctrl.id]
        if ctrl.code >= 200 and ctrl.code < 400:
            func(ctrl.params)

    if tn_globals.WaitingFor and tn_globals.WaitingFor.await_id == ctrl.id:
        if 'varname' in tn_globals.WaitingFor:
            tn_globals.Variables[tn_globals.WaitingFor.varname] = ctrl
        if tn_globals.WaitingFor.failOnError and ctrl.code >= 400:
            raise Exception(str(ctrl.code) + " " + ctrl.text)
        tn_globals.WaitingFor = None

    topic = " (" + str(ctrl.topic) + ")" if ctrl.topic else ""
    stdoutln("\r<= " + str(ctrl.code) + " " + ctrl.text + topic)


# The main processing loop: send messages to server, receive responses.
def run(args, schema, secret):
    failed = False
    try:
        if tn_globals.IsInteractive:
            tn_globals.Prompt = PromptSession()
        # Create secure channel with default credentials.
        channel = None
        if args.ssl:
            opts = (('grpc.ssl_target_name_override', args.ssl_host),) if args.ssl_host else None
            channel = grpc.secure_channel(args.host, grpc.ssl_channel_credentials(), opts)
        else:
            channel = grpc.insecure_channel(args.host)

        # Call the server
        stream = pbx.NodeStub(channel).MessageLoop(gen_message(schema, secret, args))

        # Read server responses
        for msg in stream:
            if tn_globals.Verbose:
                stdoutln("\r<= " + to_json(msg))

            if msg.HasField("ctrl"):
                handle_ctrl(msg.ctrl)

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

                if tn_globals.WaitingFor and tn_globals.WaitingFor.await_id == msg.meta.id:
                    if 'varname' in tn_globals.WaitingFor:
                        tn_globals.Variables[tn_globals.WaitingFor.varname] = msg.meta
                    tn_globals.WaitingFor = None

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
                # 'ON', 'OFF', 'UA', 'UPD', 'GONE', 'ACS', 'TERM', 'MSG', 'READ', 'RECV', 'DEL', 'TAGS'
                what = pb.ServerPres.What.Name(msg.pres.what)
                stdoutln("\r<= pres " + what + " " + msg.pres.topic)

            elif msg.HasField("info"):
                switcher = {
                    pb.READ: 'READ',
                    pb.RECV: 'RECV',
                    pb.KP: 'KP'
                }
                stdoutln("\rMessage #" + str(msg.info.seq_id) + " " + switcher.get(msg.info.what, "unknown") +
                    " by " + msg.info.from_user_id + "; topic=" + msg.info.topic + " (" + msg.topic + ")")

            else:
                stdoutln("\rMessage type not handled" + str(msg))

    except grpc.RpcError as err:
        # print(err)
        printerr("gRPC failed with {0}: {1}".format(err.code(), err.details()))
        failed = True
    except Exception as ex:
        printerr("Request failed: {0}".format(ex))
        failed = True
    finally:
        printout('Shutting down...')
        channel.close()
        if tn_globals.InputThread != None:
            tn_globals.InputThread.join(0.3)

    return 1 if failed else 0

# Read cookie file for logging in with the cookie.
def read_cookie():
    try:
        cookie = open('.tn-cli-cookie', 'r')
        params = json.load(cookie)
        cookie.close()
        return params.get("token")

    except Exception as err:
        printerr("Missing or invalid cookie file '.tn-cli-cookie'", err)
        return None

# Lambda for handling login
def handle_login(params):
    if params == None:
        return None

    # Protobuf map 'params' is a map which is not a python object or a dictionary. Convert it.
    nice = {}
    for p in params:
        nice[p] = json.loads(params[p])

    stdoutln("Authenticated as", nice.get('user'))

    tn_globals.AuthToken = nice.get('token')

    return nice

# Save cookie to file after successful login.
def save_cookie(params):
    if params == None:
        return

    try:
        cookie = open('.tn-cli-cookie', 'w')
        json.dump(handle_login(params), cookie)
        cookie.close()
    except Exception as err:
        stdoutln("Failed to save authentication cookie", err)

# Log server info.
def print_server_params(params):
    servParams = []
    for p in params:
        servParams.append(p + ": " + str(json.loads(params[p])))
    stdoutln("\r<= Connected to server: " + "; ".join(servParams))

if __name__ == '__main__':
    """Parse command-line arguments. Extract host name and authentication scheme, if one is provided"""
    version = APP_VERSION + "/" + LIB_VERSION + "; gRPC/" + GRPC_VERSION + "; Python " + platform.python_version()
    purpose = "Tinode command line client. Version " + version + "."

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
        printout(version)
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

    # Check if background session is specified explicitly. If not set it to
    # True for non-interactive sessions.
    if args.background is None and not tn_globals.IsInteractive:
        args.background = True

    sys.exit(run(args, schema, secret))
