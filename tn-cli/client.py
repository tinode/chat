"""Tinode gRPC client operations and message handling."""

from __future__ import print_function

import grpc
import json
import sys
import time

from tinode_grpc import pb
from tinode_grpc import pbx

import tn_globals
from tn_globals import printerr, stdoutln, to_json
from utils import dotdict

# 5 seconds timeout for .await/.must commands.
AWAIT_TIMEOUT = 5


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
    import random
    import threading
    from input_handler import stdin
    from commands import hiMsg, loginMsg, serialize_cmd

    random.seed()
    id = random.randint(10000,60000)

    # Asynchronous input-output
    tn_globals.InputThread = threading.Thread(target=stdin, args=(tn_globals.InputQueue,))
    tn_globals.InputThread.daemon = True
    tn_globals.InputThread.start()

    try:
        from importlib.metadata import version
    except ImportError:
        from importlib_metadata import version
    import platform

    APP_NAME = "tn-cli"
    APP_VERSION = "3.0.1"
    LIB_VERSION = version("tinode_grpc")
    GRPC_VERSION = version("grpcio")

    user_agent = APP_NAME + "/" + APP_VERSION + " (" + \
        platform.system() + "/" + platform.release() + "); gRPC-python/" + LIB_VERSION + "+" + GRPC_VERSION

    msg = hiMsg(id, args.background, user_agent, LIB_VERSION)
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


# The main processing loop: send messages to server, receive responses.
def run(args, schema, secret):
    failed = False
    try:
        from prompt_toolkit import PromptSession

        if tn_globals.IsInteractive:
            tn_globals.Prompt = PromptSession()
        # Create channel with default credentials.
        tn_globals.Connection = None
        if args.ssl:
            opts = (('grpc.ssl_target_name_override', args.ssl_host),) if args.ssl_host else None
            tn_globals.Connection = grpc.secure_channel(args.host, grpc.ssl_channel_credentials(), opts)
        else:
            tn_globals.Connection = grpc.insecure_channel(args.host)

        # Call the server
        stream = pbx.NodeStub(tn_globals.Connection).MessageLoop(gen_message(schema, secret, args))

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
                # 'ON', 'OFF', 'UA', 'UPD', 'GONE', 'ACS', 'TERM', 'MSG', 'READ', 'RECV', 'DEL', 'TAGS', 'AUX'
                what = pb.ServerPres.What.Name(msg.pres.what)
                stdoutln("\r<= pres " + what + " " + msg.pres.topic)

            elif msg.HasField("info"):
                switcher = {
                    pb.READ: 'READ',
                    pb.RECV: 'RECV',
                    pb.KP: 'KP',
                    pb.CALL: 'CALL'
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
        from tn_globals import printout
        printout('Shutting down...')
        tn_globals.Connection.close()
        if tn_globals.InputThread != None:
            tn_globals.InputThread.join(0.3)

    return 1 if failed else 0
