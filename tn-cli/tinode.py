"""The Python implementation of the gRPC Tinode client."""

import grpc

import model_pb2
import model_pb2_grpc

def gen_message():
    while True:
        cmd = input("\ntn> ")
        if cmd == "exit" or cmd == "quit":
            quit()
        else:
            yield cmd


def main_loop(stub):
    stream= stub.MessageLoop(gen_message())
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
    from sys import argv
    run(argv[1] if (len(argv) > 1 and argv[1]) else 'localhost:6061')
