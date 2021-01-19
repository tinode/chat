"""Python implementation of a Tinode chatbot."""

# For compatibility between python 2 and 3
from __future__ import print_function

import argparse
import base64
from concurrent import futures
from datetime import datetime
import json
import os
import pkg_resources
import platform
try:
    import Queue as queue
except ImportError:
    import queue
import random
import signal
import sys
import time

import grpc
from google.protobuf.json_format import MessageToDict

# Import generated grpc modules
from tinode_grpc import pb
from tinode_grpc import pbx

# For compatibility with python2
if sys.version_info[0] >= 3:
    unicode = str

APP_NAME = "Tino-chatbot"
APP_VERSION = "1.2.1"
LIB_VERSION = pkg_resources.get_distribution("tinode_grpc").version

# Maximum length of string to log. Shorten longer strings.
MAX_LOG_LEN = 64

# User ID of the current user
botUID = None

# Dictionary wich contains lambdas to be executed when server response is received
onCompletion = {}

# This is needed for gRPC ssl to work correctly.
os.environ["GRPC_SSL_CIPHER_SUITES"] = "HIGH+ECDSA"

def log(*args):
    print(datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S.%f')[:-3], *args)

# Add bundle for future execution
def add_future(tid, bundle):
    onCompletion[tid] = bundle

# Shorten long strings for logging.
def clip_long_string(obj):
    if isinstance(obj, unicode) or isinstance(obj, str):
        if len(obj) > MAX_LOG_LEN:
            return '<' + str(len(obj)) + ' bytes: ' + obj[:12] + '...' + obj[-12:] + '>'
        return obj
    elif isinstance(obj, (list, tuple)):
        return [clip_long_string(item) for item in obj]
    elif isinstance(obj, dict):
        return dict((key, clip_long_string(val)) for key, val in obj.items())
    else:
        return obj

def to_json(msg):
    return json.dumps(clip_long_string(MessageToDict(msg)))

# Resolve or reject the future
def exec_future(tid, code, text, params):
    bundle = onCompletion.get(tid)
    if bundle != None:
        del onCompletion[tid]
        try:
            if code >= 200 and code < 400:
                arg = bundle.get('arg')
                bundle.get('onsuccess')(arg, params)
            else:
                log("Error: {} {} ({})".format(code, text, tid))
                onerror = bundle.get('onerror')
                if onerror:
                    onerror(bundle.get('arg'), {'code': code, 'text': text})
        except Exception as err:
            log("Error handling server response", err)

# List of active subscriptions
subscriptions = {}
def add_subscription(topic):
    subscriptions[topic] = True

def del_subscription(topic):
    subscriptions.pop(topic, None)

def subscription_failed(topic, errcode):
    if topic == 'me':
        # Failed 'me' subscription means the bot is disfunctional.
        if errcode.get('code') == 502:
            # Cluster unreachable. Break the loop and retry in a few seconds.
            client_post(None)
        else:
            exit(1)

def login_error(unused, errcode):
    # Check for 409 "already authenticated".
    if errcode.get('code') != 409:
        exit(1)

def server_version(params):
    if params == None:
        return
    log("Server:", params['build'].decode('ascii'), params['ver'].decode('ascii'))

def next_id():
    next_id.tid += 1
    return str(next_id.tid)
next_id.tid = 100

# Quotes from the fortune cookie file
quotes = []

def next_quote():
    idx = random.randrange(0, len(quotes))
    # Make sure quotes are not repeated
    while idx == next_quote.idx:
        idx = random.randrange(0, len(quotes))
    next_quote.idx = idx
    return quotes[idx]
next_quote.idx = 0

# This is the class for the server-side gRPC endpoints
class Plugin(pbx.PluginServicer):
    def Account(self, acc_event, context):
        action = None
        if acc_event.action == pb.CREATE:
            action = "created"
            # TODO: subscribe to the new user.

        elif acc_event.action == pb.UPDATE:
            action = "updated"
        elif acc_event.action == pb.DELETE:
            action = "deleted"
        else:
            action = "unknown"

        log("Account", action, ":", acc_event.user_id, acc_event.public)

        return pb.Unused()

queue_out = queue.Queue()

def client_generate():
    while True:
        msg = queue_out.get()
        if msg == None:
            return
        log("out:", to_json(msg))
        yield msg

def client_post(msg):
    queue_out.put(msg)

def client_reset():
    # Drain the queue
    try:
        while queue_out.get(False) != None:
            pass
    except queue.Empty:
        pass

def hello():
    tid = next_id()
    add_future(tid, {
        'onsuccess': lambda unused, params: server_version(params),
    })
    return pb.ClientMsg(hi=pb.ClientHi(id=tid, user_agent=APP_NAME + "/" + APP_VERSION + " (" +
        platform.system() + "/" + platform.release() + "); gRPC-python/" + LIB_VERSION,
        ver=LIB_VERSION, lang="EN"))

def login(cookie_file_name, scheme, secret):
    tid = next_id()
    add_future(tid, {
        'arg': cookie_file_name,
        'onsuccess': lambda fname, params: on_login(fname, params),
        'onerror': lambda unused, errcode: login_error(unused, errcode),
    })
    return pb.ClientMsg(login=pb.ClientLogin(id=tid, scheme=scheme, secret=secret))

def subscribe(topic):
    tid = next_id()
    add_future(tid, {
        'arg': topic,
        'onsuccess': lambda topicName, unused: add_subscription(topicName),
        'onerror': lambda topicName, errcode: subscription_failed(topicName, errcode),
    })
    return pb.ClientMsg(sub=pb.ClientSub(id=tid, topic=topic))

def leave(topic):
    tid = next_id()
    add_future(tid, {
        'arg': topic,
        'onsuccess': lambda topicName, unused: del_subscription(topicName)
    })
    return pb.ClientMsg(leave=pb.ClientLeave(id=tid, topic=topic))

def publish(topic, text):
    tid = next_id()
    return pb.ClientMsg(pub=pb.ClientPub(id=tid, topic=topic, no_echo=True,
        head={"auto": json.dumps(True).encode('utf-8')}, content=json.dumps(text).encode('utf-8')))

def note_read(topic, seq):
    return pb.ClientMsg(note=pb.ClientNote(topic=topic, what=pb.READ, seq_id=seq))

def init_server(listen):
    # Launch plugin server: acception connection(s) from the Tinode server.
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pbx.add_PluginServicer_to_server(Plugin(), server)
    server.add_insecure_port(listen)
    server.start()

    log("Plugin server running at '"+listen+"'")

    return server

def init_client(addr, schema, secret, cookie_file_name, secure, ssl_host):
    log("Connecting to", "secure" if secure else "", "server at", addr,
        "SNI="+ssl_host if ssl_host else "")

    channel = None
    if secure:
        opts = (('grpc.ssl_target_name_override', ssl_host),) if ssl_host else None
        channel = grpc.secure_channel(addr, grpc.ssl_channel_credentials(), opts)
    else:
        channel = grpc.insecure_channel(addr)

    # Call the server
    stream = pbx.NodeStub(channel).MessageLoop(client_generate())

    # Session initialization sequence: {hi}, {login}, {sub topic='me'}
    client_post(hello())
    client_post(login(cookie_file_name, schema, secret))
    client_post(subscribe('me'))

    return stream

def client_message_loop(stream):
    try:
        # Read server responses
        for msg in stream:
            log(datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S.%f')[:-3], "in:", to_json(msg))

            if msg.HasField("ctrl"):
                # Run code on command completion
                exec_future(msg.ctrl.id, msg.ctrl.code, msg.ctrl.text, msg.ctrl.params)

            elif msg.HasField("data"):
                # log("message from:", msg.data.from_user_id)

                # Protection against the bot talking to self from another session.
                if msg.data.from_user_id != botUID:
                    # Respond to message.
                    # Mark received message as read
                    client_post(note_read(msg.data.topic, msg.data.seq_id))
                    # Insert a small delay to prevent accidental DoS self-attack.
                    time.sleep(0.1)
                    # Respond with a witty quote
                    client_post(publish(msg.data.topic, next_quote()))

            elif msg.HasField("pres"):
                # log("presence:", msg.pres.topic, msg.pres.what)
                # Wait for peers to appear online and subscribe to their topics
                if msg.pres.topic == 'me':
                    if (msg.pres.what == pb.ServerPres.ON or msg.pres.what == pb.ServerPres.MSG) \
                            and subscriptions.get(msg.pres.src) == None:
                        client_post(subscribe(msg.pres.src))
                    elif msg.pres.what == pb.ServerPres.OFF and subscriptions.get(msg.pres.src) != None:
                        client_post(leave(msg.pres.src))

            else:
                # Ignore everything else
                pass

    except grpc._channel._Rendezvous as err:
        log("Disconnected:", err)

def read_auth_cookie(cookie_file_name):
    """Read authentication token from a file"""
    cookie = open(cookie_file_name, 'r')
    params = json.load(cookie)
    cookie.close()
    schema = params.get("schema")
    secret = None
    if schema == None:
        return None, None
    if schema == 'token':
        secret = base64.b64decode(params.get('secret').encode('utf-8'))
    else:
        secret = params.get('secret').encode('utf-8')
    return schema, secret

def on_login(cookie_file_name, params):
    """Save authentication token to file"""
    if params == None or cookie_file_name == None:
        return

    if 'user' in params:
        botUID = params['user'].decode("ascii")

    # Protobuf map 'params' is not a python object or dictionary. Convert it.
    nice = {'schema': 'token'}
    for key_in in params:
        if key_in == 'token':
            key_out = 'secret'
        else:
            key_out = key_in
        nice[key_out] = json.loads(params[key_in].decode('utf-8'))

    try:
        cookie = open(cookie_file_name, 'w')
        json.dump(nice, cookie)
        cookie.close()
    except Exception as err:
        log("Failed to save authentication cookie", err)

def load_quotes(file_name):
    with open(file_name) as f:
        for line in f:
            quotes.append(line.strip())

    return len(quotes)

def run(args):
    schema = None
    secret = None

    if args.login_token:
        """Use token to login"""
        schema = 'token'
        secret = args.login_token.encode('ascii')
        log("Logging in with token", args.login_token)

    elif args.login_basic:
        """Use username:password"""
        schema = 'basic'
        secret = args.login_basic.encode('utf-8')
        log("Logging in with login:password", args.login_basic)

    else:
        """Try reading the cookie file"""
        try:
            schema, secret = read_auth_cookie(args.login_cookie)
            log("Logging in with cookie file", args.login_cookie)
        except Exception as err:
            log("Failed to read authentication cookie", err)

    if schema:
        # Load random quotes from file
        log("Loaded {} quotes".format(load_quotes(args.quotes)))

        # Start Plugin server
        server = init_server(args.listen)

        # Initialize and launch client
        client = init_client(args.host, schema, secret, args.login_cookie, args.ssl, args.ssl_host)

        # Setup closure for graceful termination
        def exit_gracefully(signo, stack_frame):
            log("Terminated with signal", signo)
            server.stop(0)
            client.cancel()
            sys.exit(0)

        # Add signal handlers
        signal.signal(signal.SIGINT, exit_gracefully)
        signal.signal(signal.SIGTERM, exit_gracefully)

        # Run blocking message loop in a cycle to handle
        # server being down.
        while True:
            client_message_loop(client)
            time.sleep(3)
            client_reset()
            client = init_client(args.host, schema, secret, args.login_cookie, args.ssl, args.ssl_host)

        # Close connections gracefully before exiting
        server.stop(None)
        client.cancel()

    else:
        log("Error: authentication scheme not defined")


if __name__ == '__main__':
    """Parse command-line arguments. Extract server host name, listen address, authentication scheme"""
    random.seed()

    purpose = "Tino, Tinode's chatbot."
    log(purpose)
    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:16060', help='address of Tinode server gRPC endpoint')
    parser.add_argument('--ssl', action='store_true', help='use SSL to connect to the server')
    parser.add_argument('--ssl-host', help='SSL host name to use instead of default (useful for connecting to localhost)')
    parser.add_argument('--listen', default='0.0.0.0:40051', help='address to listen on for incoming Plugin API calls')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', default='.tn-cookie', help='read credentials from the provided cookie file')
    parser.add_argument('--quotes', default='quotes.txt', help='file with messages for the chatbot to use, one message per line')
    args = parser.parse_args()

    run(args)
