"""Python implementation of a Tinode chatbot."""

import argparse
import base64
from concurrent import futures
from Queue import Queue
import time

import grpc

import model_pb2 as pb
import model_pb2_grpc as pbx

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# Dictionary wich contains lambdas to be executed when server response is received
onCompletion = {}
# List of active subscriptions
subscriptions = {}

def next_id():
    next_id.id += 1
    return str(next_id.id)
next_id.id = 0

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
    return pb.ClientMsg(sub=pb.ClientSub(id=id, topic=topic))

def leave(id, topic):
    id = next_id()
    return pb.ClientMsg(leave=pb.ClientLeave(id=id, topic=topic))

def publish(id, topic, text):
    id = next_id()
    return pb.ClientMsg(pub=pb.ClientPub(id=id, topic=topic, no_echo=True, content=json.dumps(text)))

def client(addr, schema, secret, server, cookie_file_name):
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
                # Respond to message
                print "\nFrom: " + msg.data.from_user_id + ":\n"
                print json.loads(msg.data.content) + "\n"
            elif msg.HasField("pres"):
                # Check for online/offline status of a peer
                pass
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

def read_cookie(cookie_file_name):
    """Read authentication token from a file"""
    try:
        cookie = open(cookie_file_name, 'r')
        params = json.load(cookie)
        cookie.close()
        if params.get("token") == None:
            return None
        return base64.b64decode(params.get('token').encode('ascii'))

    except Exception as err:
        print "Missing or invalid cookie file '" + cookie_file_name + "'", err
        return None

if __name__ == '__main__':
    """Parse command-line arguments. Extract server host name, listen address, authentication scheme"""
    purpose = "Tino, Tinode's dumb chatbot."
    print purpose
    parser = argparse.ArgumentParser(description=purpose)
    parser.add_argument('--host', default='localhost:6061', help='address of Tinode server')
    parser.add_argument('--listen', default='localhost:40051', help='address to listen for incoming Plugin API calls')
    parser.add_argument('--login-basic', help='login using basic authentication username:password')
    parser.add_argument('--login-token', help='login using token authentication')
    parser.add_argument('--login-cookie', default='.tn-cookie', help='read token from the cookie file and use it for authentication')
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
        secret = read_cookie(args.login_cookie)
        if secret != None:
            schema = 'token'

    if schema != None:
        # Start Plugin server
        server(args.listen)
        # Initialize and launch client
        client(args.host, schema, secret, server, args.login_cookie)
    else:
        print "Error: unknown authentication scheme"
