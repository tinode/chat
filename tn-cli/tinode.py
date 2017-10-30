"""The Python implementation of the gRPC Tinode client."""

import sys
import grpc
import random

import model_pb2
import model_pb2_grpc

VERSION = 13

def gen_message():
    random.seed()
    id = random.randint(10000,99999)
    yield model_pb2.ClientMsg(hi=model_pb2.ClientHi(id=str(id), user_agent="tn-cli/0.13 python gRPC", ver=VERSION, lang="EN"))
    while True:
        id+=1
        cmd = raw_input("tn> ")
        if cmd == "":
            continue
        if cmd == "exit" or cmd == "quit":
            return
        else:
            yield cmd


def main_loop(stub):
    stream = stub.MessageLoop(gen_message())
    try:
        for r in stream:
            print(r)
    except grpc._channel._Rendezvous as err:
        print(err)


def run(addr):
    channel = grpc.insecure_channel(addr)
    stub = model_pb2_grpc.NodeStub(channel)
    main_loop(stub)


if __name__ == '__main__':
    print("Tinode command line client. Version 0.13")
    run(sys.argv[1] if (len(sys.argv) > 1 and sys.argv[1]) else 'localhost:6061')
