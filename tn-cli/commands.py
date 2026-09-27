"""Command parsing and message construction for tn-cli."""

from __future__ import print_function

import argparse
import base64
import json
import mimetypes
import os
import re
import requests
import shlex
import threading
import time

from tinode_grpc import pb
from tinode_grpc import pbx

import tn_globals
from tn_globals import printout, stdoutln
from utils import (
    makeTheCard, inline_image, attachment, encode_to_bytes,
    parse_cred, parse_trusted, dotdict, DELETE_MARKER, TINODE_DEL
)

APP_NAME = "tn-cli"
APP_VERSION = "3.0.1"
PROTOCOL_VERSION = "0"

# Regex to match and parse subscripted entries in variable paths.
RE_INDEX = re.compile(r"(\w+)\[(\w+)\]")

# Macros module (may be None).
macros = None


def set_macros_module(m):
    """Set the macros module for use in command parsing."""
    global macros
    macros = m


# Create proto for ClientExtra
def pack_extra(cmd):
    return pb.ClientExtra(on_behalf_of=tn_globals.DefaultUser, auth_level=pb.ROOT if cmd.as_root else pb.NONE)


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


# Constructing individual messages
# {hi}
def hiMsg(id, background, user_agent, lib_version):
    tn_globals.OnCompletion[str(id)] = lambda params: print_server_params(params)
    return pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent=user_agent,
        ver=lib_version, lang="EN", background=background))


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
            public=cmd.public, private=cmd.private, trusted=encode_to_bytes(parse_trusted(cmd.trusted))),
        cred=parse_cred(cmd.cred)),
        extra=pack_extra(cmd))


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

    from client import handle_login, save_cookie
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
            desc=pb.SetDesc(public=cmd.public, private=cmd.private,
                            trusted=encode_to_bytes(parse_trusted(cmd.trusted)),
                            default_acs=pb.DefaultAcsMode(auth=cmd.auth, anon=cmd.anon)),
            sub=pb.SetSub(mode=cmd.mode),
            tags=cmd.tags.split(",") if cmd.tags else None),
        get_query=cmd.get_query),
        extra=pack_extra(cmd))


# {leave}
def leaveMsg(id, cmd, ignored):
    if not cmd.topic:
        cmd.topic = tn_globals.DefaultTopic
    return pb.ClientMsg(leave=pb.ClientLeave(id=str(id), topic=cmd.topic, unsub=cmd.unsub),
        extra=pack_extra(cmd))


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
        extra=pack_extra(cmd))


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
        extra=pack_extra(cmd))


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
                public=cmd.public, private=cmd.private,
                trusted=encode_to_bytes(parse_trusted(cmd.trusted))),
        sub=pb.SetSub(user_id=cmd.user, mode=cmd.mode),
        tags=cmd.tags.split(",") if cmd.tags else None,
        cred=cred)),
        extra=pack_extra(cmd))


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

    msg = pb.ClientMsg(extra=pack_extra(cmd))
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
        extra=pack_extra(cmd))


# Upload file out of band over HTTP(S) (not gRPC).
def upload(id, cmd, args):
    try:
        from client import handle_ctrl
        scheme = 'https' if args.ssl else 'http'
        try:
            from importlib.metadata import version
        except ImportError:
            from importlib_metadata import version
        LIB_VERSION = version("tinode_grpc")

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


def fileUpload(id, cmd, args):
    def iter_file(filepath, size=1024*1024):
        _, name = os.path.split(filepath)
        mimeType = mimetypes.guess_type(filepath)[0]
        with open(filepath, mode='rb') as fd:
            try:
                yield pb.FileUpReq(id=str(id), auth=pb.Auth(scheme='token', secret=tn_globals.AuthToken),
                                   topic="", meta=pb.FileMeta(name=name, mime_type=mimeType, size=0))
                while True:
                    chunk = fd.read(size)
                    if chunk:
                        yield pb.FileUpReq(content=chunk)
                    else:  # Finished.
                        break
            except Exception as ex:
                stdoutln("Failed to read '{0}':".format(cmd.filename), ex)

    try:
        response = pbx.NodeStub(tn_globals.Connection).LargeFileReceive(iter_file(cmd.filename))
        if response.code == 200:
            stdoutln("Upload OK: '{0}' ({1}), size={2}"
                     .format(response.meta.name, response.meta.mime_type, response.meta.size))
        else:
            stdoutln("Upload failed: {0} {1}".format(response.code, response.text))
    except Exception as ex:
        stdoutln("Failed to upload '{0}':".format(cmd.filename), ex)


def fileDownload(id, cmd, args):
    req = pb.FileDownReq(id=str(id), auth=pb.Auth(scheme='token', secret=tn_globals.AuthToken),
                         uri=cmd.filename, if_modified="")

    # Call the server
    stream = pbx.NodeStub(tn_globals.Connection).LargeFileServe(req)
    # Read file chunks
    fd = None
    for chunk in stream:
        if chunk:
            if chunk.code >= 400:
                stdoutln("Failed to download '{0}': {1} {2}".format(cmd.filename, chunk.code, chunk.text))
                break
            if chunk.code >= 300:
                stdoutln("Use HTTP {0} to download from {1}".format(chunk.code, chunk.redir_url))
                break
            if not fd:
                fd = open(chunk.meta.name, mode='wb')
            fd.write(chunk.content)
            continue
    if fd:
        fd.close()


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
    elif parts[0] == "file":
        parser = argparse.ArgumentParser(prog=parts[0], description='Download or upload a large file')
        parser.add_argument('--what', default='down', choices=['down', 'up'], help='download \'down\' or upload \'up\'')
        parser.add_argument('filename', help='name of the file to upload')
    elif parts[0] == "get":
        parser = argparse.ArgumentParser(prog=parts[0], description='Query topic for messages or metadata')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to query')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to query')
        parser.add_argument('--desc', action='store_true', help='query topic description')
        parser.add_argument('--sub', action='store_true', help='query topic subscriptions')
        parser.add_argument('--tags', action='store_true', help='query topic tags')
        parser.add_argument('--data', action='store_true', help='query topic messages')
        parser.add_argument('--cred', action='store_true', help='query account credentials')
    elif parts[0] == "leave":
        parser = argparse.ArgumentParser(prog=parts[0], description='Detach or unsubscribe from topic')
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to detach from')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to detach from')
        parser.add_argument('--unsub', action='store_true', help='detach and unsubscribe from topic')
    elif parts[0] == "login":
        parser = argparse.ArgumentParser(prog=parts[0], description='Authenticate current session')
        parser.add_argument('secret', nargs='?', default=argparse.SUPPRESS, help='secret for authentication')
        parser.add_argument('--scheme', default='basic', help='authentication schema, default=basic')
        parser.add_argument('--secret', dest='secret', default=None, help='secret for authentication')
        parser.add_argument('--uname', default=None, help='user name in basic authentication scheme')
        parser.add_argument('--password', default=None, help='password in basic authentication scheme')
        parser.add_argument('--cred', default=None, help='credentials, comma separated list in method:value:response format, e.g. email:test@example.com,tel:12345')
    elif parts[0] == "note":
        parser = argparse.ArgumentParser(prog=parts[0], description='Send notification to topic, ex "note kp"')
        parser.add_argument('topic', help='topic to notify')
        parser.add_argument('what', nargs='?', default='kp', const='kp', choices=['call', 'kp', 'read', 'recv'],
            help='notification type: kp (key press), recv, read - message received or read receipt')
        parser.add_argument('--seq', help='message ID being reported')
        parser.add_argument('--event', help='video call event', choices=['accept', 'answer', 'ice-candidate', 'hang-up', 'offer', 'ringing'])
        parser.add_argument('--payload', help='video call payload')
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
    elif parts[0] == "upload":
        parser = argparse.ArgumentParser(prog=parts[0], description='Upload file out of band over HTTP(S)')
        parser.add_argument('filename', help='name of the file to upload')
    elif macros:
        parser = macros.parse_macro(parts)

    if parser:
        try:
            parser.add_argument('--as_root', action='store_true', help='execute command at ROOT auth level')
        except Exception:
            # Ignore exception here: --as_root has been added already, macro parser is persistent.
            pass
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
        printout("\tfile\t\t- download or upload a large file")
        printout("\tget\t\t- query topic for metadata or messages")
        printout("\tleave\t\t- detach or unsubscribe from topic")
        printout("\tlogin\t\t- authenticate current session")
        printout("\tnote\t\t- send a notification")
        printout("\tpub\t\t- post message to topic")
        printout("\tset\t\t- update topic metadata")
        printout("\tsub\t\t- subscribe to topic")
        printout("\tupload\t\t- upload file out of band over HTTP(S)")
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
    global DELETE_MARKER

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

        elif cmd.cmd == "file":
            # Start async upload
            target = fileUpload if cmd.what == 'up' else fileDownload
            upload_thread = threading.Thread(target=target, args=(id, derefVals(cmd), args), name="file_"+cmd.filename)
            upload_thread.start()
            cmd.no_yield = True
            return True, cmd

        # Process dictionary
        elif cmd.cmd == ".log":
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
        stdoutln("Error in '{0}': {1}".format(string, err))
        return None, None


# Log server info.
def print_server_params(params):
    servParams = []
    for p in params:
        servParams.append(p + ": " + str(json.loads(params[p])))
    stdoutln("\r<= Connected to server: " + "; ".join(servParams))
