"""Python implementation of a Tinode chatbot."""

import argparse
import base64
from concurrent import futures
from Queue import Queue
import random
import time

import grpc

import model_pb2 as pb
import model_pb2_grpc as pbx

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# Dictionary wich contains lambdas to be executed when server response is received
onCompletion = {}
# Add bundle for future execution
def add_future(id, bundle):
    onCompletion[id] = bundle

# Resolve or reject the future
def exec_future(id, code):
    bundle = onCompletion.get(id)
    if bundle != None:
        del onCompletion[id]
        if code >= 200 and code < 400:
            bundle.action(bundle.arg)

# List of active subscriptions
subscriptions = {}
def add_subscription(topic):
    subscriptions[topic] = True

def del_subscription(topic):
    subscriptions.pop(topic, None)

# Quotes from the fortune cookie file
quotes = []

def next_id():
    next_id.id += 1
    return str(next_id.id)
next_id.id = 100

def next_quote():
    idx = random.randrange(0, len(quotes))
    while idx == next_quote.idx:
        idx = random.randrange(0, len(quotes))
    next_quote.idx = idx
    return quotes[idx]

class Plugin(pbx.PluginServicer):
    def Account(self, acc_event, context):
        action = None
        if acc_event.action == pb.CREATED:
            action = "created"
        elif acc_event.action == pb.UPDATED:
            action = "updated"
        else:
            action = "deleted"

        print "New acount", action, ":", acc_event.user_id, acc_event.public
        return pb.Unused()


queue_out = Queue()

def client_generate():
    while True:
        msg = queue_out.get()
        if msg == None:
            return
        yield msg

def client_post(msg):
    queue_out.put(msg)

def subscribe(topic):
    id = next_id()
    onCompletion[id] = {
        arg: topic,
        action: lambda x: add_subscription(x)
    }
    return pb.ClientMsg(sub=pb.ClientSub(id=id, topic=topic))

def leave(topic):
    id = next_id()
    onCompletion[id] = {
        arg: topic,
        action: lambda x: del_subscription(x)
    }
    return pb.ClientMsg(leave=pb.ClientLeave(id=id, topic=topic))

def publish(topic, text):
    id = next_id()
    return pb.ClientMsg(pub=pb.ClientPub(id=id, topic=topic, no_echo=True, content=json.dumps(text)))

def client(addr, schema, secret, server, cookie_file_name):
    channel = grpc.insecure_channel(addr)
    stub = pbx.NodeStub(channel)
    # Call the server
    stream = stub.MessageLoop(client_generate())

    # Send: {hi}, {login}, {sub topic='me'}

    try:
        # Read server responses
        for msg in stream:
            if msg.HasField("ctrl"):
                # Run code on command completion
                exec_future(msg.ctrl.id, msg.ctrl.code)
                print str(msg.ctrl.code) + " " + msg.ctrl.text

            elif msg.HasField("data"):
                # Respond to message.
                print "\nFrom: " + msg.data.from_user_id + ":\n"
                client_post(publish(msg.data.topic, next_quote()))

            elif msg.HasField("pres"):
                # Wait for peers to appear online and subscribe to their topics
                if msg.pres.topic == 'me':
                    if msg.pres.what == pb.ServerPres.ON and subscriptions.get(msg.pres.src) == None:
                        client_post(subscribe(msg.pres.src))
                    elif msg.pres.what == pb.ServerPres.OFF and subscriptions.get(msg.pres.src) != None:
                        client_post(leave(msg.pres.src))

            else:
                # Ignore everything else
                pass

    except grpc._channel._Rendezvous as err:
        print err
    except KeyboardInterrupt:
        queue_out.put(None)
        server.stop(0)


def server(listen):
    # Launch plugin: acception connection(s) from the Tinode server.
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pbx.add_PluginServicer_to_server(Plugin(), server)
    server.add_insecure_port(listen)
    server.start()

def read_auth_cookie(cookie_file_name):
    """Read authentication token from a file"""
    try:
        cookie = open(cookie_file_name, 'r')
        params = json.load(cookie)
        cookie.close()
        if params.get("token") == None:
            return None
        return base64.b64decode(params.get('token').encode('ascii'))

    except Exception as err:
        print "Failed to read authentication cookie", err
        return None

def save_auth_cookie(cookie_file_name, params):
    """Save authentication token to file"""
    if params == None or cookie_file_name == None:
        return

    # Protobuf map 'params' is not a python object or dictionary. Convert it.
    nice = {}
    for p in params:
        nice[p] = json.loads(params[p])

    try:
        cookie = open(cookie_file_name, 'w')
        json.dump(nice, cookie)
        cookie.close()
    except Exception as err:
        print "Failed to save authentication cookie", err

def load_quotes(file_name):
    with open(file_name) as f:
        for line in f:
            quotes.append(line.strip())

    return len(quotes)

if __name__ == '__main__':
    """Parse command-line arguments. Extract server host name, listen address, authentication scheme"""
    random.seed()

    purpose = "Tino, Tinode's dumb chatbot."
    print purpose
    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:6061', help='address of Tinode server')
    parser.add_argument('--listen', default='localhost:40051', help='address to listen for incoming Plugin API calls')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', default='.tn-cookie', help='read token from the cookie file and use it for authentication')
    parser.add_argument('--quotes', default='quotes.txt', help='file with messages for the chatbot to use, one message per line')
    args = parser.parse_args()

    schema = None
    secret = None

    if args.login_token != None:
        """Use token to login"""
        schema = 'token'
        secret = args.login_token
    elif args.login_basic != None:
        """Use username:password"""
        schema = 'basic'
        secret = args.login_basic
    else:
        """Try reading the cookie file"""
        secret = read_auth_cookie(args.login_cookie)
        if secret != None:
            schema = 'token'

    if schema != None:
        # Load random quotes from file
        print "Loaded {} quotes".format(load_quotes(args.quotes))

        # Start Plugin server
        server(args.listen)
        # Initialize and launch client
        client(args.host, schema, secret, server, args.login_cookie)
    else:
        print "Error: unknown authentication scheme"
