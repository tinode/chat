"""The Python implementation of the gRPC Tinode client."""

import grpc
import json
import random
import sys

import model_pb2 as pb
import model_pb2_grpc as pbx

VERSION = 13

def parse_command(string, id):
    """Parses command line input into a ClientMsg"""
    parts = string.split(" ")
    if parts[0] == "acc":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "login":
        return pb.ClientMsg(login=pb.ClientLogin(id=str(id),
            scheme=("basic", "token")[parts[1].find(":") == -1],
            secret=parts[1]))
    elif parts[0] == "sub":
        return pb.ClientMsg(sub=pb.ClientSub(id=str(id), topic=parts[1]))
    elif parts[0] == "leave":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "pub":
        return pb.ClientMsg(pub=pb.ClientPub(id=str(id), topic=parts[1], no_echo=True, content=parts[2]))
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

def gen_message():
    """Generator: reads user input, converts to ClientMsg, and yields"""

    random.seed()
    id = random.randint(10000,60000)
    yield pb.ClientMsg(hi=pb.ClientHi(id=str(id), user_agent="tn-cli/0.13 python gRPC", ver=VERSION, lang="EN"))

    while True:
        id+=1
        inp = raw_input("tn> ")
        if inp == "":
            continue
        if inp == "exit" or inp == "quit":
            return
        cmd = parse_command(inp, id)
        if cmd != None:
            yield cmd


def main_loop(stub):
    stream = stub.MessageLoop(gen_message())
    try:
        for msg in stream:
            if msg.ctrl != None:
                print("\n" + str(msg.ctrl.code) + " " + msg.ctrl.text)
                if msg.ctrl.params != None:
                    for param in msg.ctrl.params:
                        print(param + ": " + json.loads(msg.ctrl.params[param]))
            else:
                print(msg)
    except grpc._channel._Rendezvous as err:
        print(err)


def run(addr):
    channel = grpc.insecure_channel(addr)
    stub = pbx.NodeStub(channel)
    main_loop(stub)


if __name__ == '__main__':
    print("Tinode command line client. Version 0.13")
    run(sys.argv[1] if (len(sys.argv) > 1 and sys.argv[1]) else 'localhost:6061')
